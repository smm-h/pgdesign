package migrate

// Store-backed rendering for self-contained ops (roadmap 5.1).
//
// RenderSQL resolves the op's payload from the object store and produces its
// true SQL. There are NO FALLBACKS: unlike the legacy DDLOp path, a payload is
// always present (BuildOp/ParseOp guarantee it), so the deny-mutation /
// append-only / "-- unknown op" comment-stub degradations are unrepresentable.
// PGVersion is honored UNIFORMLY: every op renders under its recorded
// PGVersion (opBody.PGVersion), never a hardcoded zero, and create_table
// resolves enum/domain qualification from its recorded type closure.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/smm-h/pgdesign/internal/enc"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/objstore"
	"github.com/smm-h/pgdesign/internal/sql"
)

// RenderSQL resolves the op payload from the store and renders the op's SQL.
func (o SelfContainedOp) RenderSQL(store *objstore.Store) (string, error) {
	body, err := loadBody(store, o.payload)
	if err != nil {
		return "", err
	}
	return renderBody(store, body)
}

// renderBody renders an op body to SQL, resolving def and blob references from
// the store. Every branch honors body.PGVersion; create_table additionally
// resolves the enum/domain closure. An unhandled kind with no blob is a hard
// error, never a comment stub.
func renderBody(store *objstore.Store, b opBody) (string, error) {
	// Delta-rendered ops (nested-modifier / alter / drop / rls / rename) render
	// by re-invoking the SINGLE SQL oracle OpToSQL on the stored scalar delta —
	// byte-identical to generate by construction, no re-implemented SQL here.
	if b.Delta != nil {
		return OpToSQL(*b.Delta), nil
	}
	switch b.Kind {
	case "create_table":
		tbl, err := decodeTableDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		enums, err := decodeEnumClosure(store, b.EnumIDs)
		if err != nil {
			return "", err
		}
		domains, err := decodeDomainClosure(store, b.DomainIDs)
		if err != nil {
			return "", err
		}
		// Fold COMMENT ON emission into the create_table render: the decoded table
		// def carries the table and column comments, and the chain apply path has no
		// separate comment section (unlike internal/generate's full-DDL path), so a
		// chain-created table would otherwise land WITHOUT its mandatory comment.
		ddl := sql.CreateTable(&tbl, b.Schema, false, b.PGVersion, enums, domains)
		if comments := sql.CommentsOnTable(b.Schema, &tbl); len(comments) > 0 {
			ddl += "\n" + strings.Join(comments, "\n")
		}
		return ddl, nil

	case "create_view", "create_or_replace_view":
		v, err := decodeViewDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.CreateView(b.Schema, &v, b.Replace), nil

	case "create_materialized_view":
		mv, err := decodeMatViewDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.CreateMaterializedView(b.Schema, &mv, false), nil

	case "create_sequence":
		s, err := decodeSequenceDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.CreateSequence(b.Schema, &s, false), nil

	case "alter_sequence":
		s, err := decodeSequenceDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.AlterSequence(b.Schema, &s), nil

	case "create_composite_type":
		c, err := decodeCompositeDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.CreateCompositeType(b.Schema, c, false), nil

	case "create_domain":
		d, err := decodeDomainDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.CreateDomain(b.Schema, d, false), nil

	case "create_function", "create_or_replace_function":
		f, err := decodeFunctionDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.CreateFunction(b.Schema, f), nil

	case "create_trigger":
		t, err := decodeTriggerDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		schema, tableName := splitQualifiedName(b.Table)
		return sql.CreateTrigger(schema, tableName, t, false, b.PGVersion), nil

	case "create_policy":
		p, err := decodePolicyDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		schema, tableName := splitQualifiedName(b.Table)
		return sql.CreatePolicy(schema, tableName, p, false, b.PGVersion), nil

	case "create_partition":
		spec, err := decodePartitionSpec(store, b.DefID)
		if err != nil {
			return "", err
		}
		schema, parentName := splitQualifiedName(b.ParentTable)
		return sql.CreatePartitionOf(schema, &spec, parentName, false), nil

	case "create_enum":
		e, err := decodeEnumDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.CreateEnum(e.Schema, e.Name, e.Values, false), nil

	case "schema_meta":
		meta, err := decodeSchemaMetaDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return renderSchemaMeta(meta), nil

	// ---- down / leaf ops (mirror the legacy OpToSQL drop renderers exactly) ----
	case "drop_table":
		return fmt.Sprintf("DROP TABLE %s;", sql.QualifiedName(b.Schema, b.Name)), nil
	case "drop_view":
		return sql.DropView(b.Schema, b.Name, false), nil
	case "drop_materialized_view":
		return sql.DropMaterializedView(b.Schema, b.Name, false), nil
	case "drop_sequence":
		return sql.DropSequence(b.Schema, b.Name, false), nil
	case "drop_composite_type":
		return sql.DropCompositeType(b.Schema, b.Name, true), nil
	case "drop_domain":
		return sql.DropDomain(b.Schema, b.Name, true), nil
	case "drop_function":
		f, err := decodeFunctionDef(store, b.DefID)
		if err != nil {
			return "", err
		}
		return sql.DropFunction(b.Schema, f, false), nil
	case "drop_trigger":
		schema, tableName := splitQualifiedName(b.Table)
		return fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;",
			sql.QuoteIdent(b.Name), sql.QualifiedName(schema, tableName)), nil
	case "drop_policy":
		schema, tableName := splitQualifiedName(b.Table)
		return sql.DropPolicy(schema, tableName, b.Name), nil

	default:
		// RawSQL-bodied and DML families (create_sm_trigger, create_partman_*,
		// backfill, transform, and their raw downs) render their opaque blob.
		if b.BlobID != "" {
			blob, err := store.Get(b.BlobID)
			if err != nil {
				return "", fmt.Errorf("migrate: op blob %s does not resolve: %w", b.BlobID, err)
			}
			return string(blob), nil
		}
		return "", fmt.Errorf("migrate: self-contained op %q has no renderer and no blob payload", b.Kind)
	}
}

// ---- def encode/decode helpers ----
//
// Object types with a canonical per-object encoder (internal/enc) are stored
// via that encoder; Trigger, Policy, and PartitionSpec (which have no top-level
// enc encoder — they are nested in a table's form) are stored as canonical JSON
// of the model struct.

func decodeTableDef(store *objstore.Store, id string) (model.Table, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.Table{}, err
	}
	return enc.DecodeTable(data)
}

func decodeViewDef(store *objstore.Store, id string) (model.View, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.View{}, err
	}
	return enc.DecodeView(data)
}

func decodeMatViewDef(store *objstore.Store, id string) (model.MaterializedView, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.MaterializedView{}, err
	}
	return enc.DecodeMaterializedView(data)
}

func decodeSequenceDef(store *objstore.Store, id string) (model.Sequence, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.Sequence{}, err
	}
	return enc.DecodeSequence(data)
}

func decodeFunctionDef(store *objstore.Store, id string) (model.Function, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.Function{}, err
	}
	return enc.DecodeFunction(data)
}

func decodeDomainDef(store *objstore.Store, id string) (model.Domain, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.Domain{}, err
	}
	return enc.DecodeDomain(data)
}

func decodeEnumDef(store *objstore.Store, id string) (model.Enum, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.Enum{}, err
	}
	return enc.DecodeEnum(data)
}

func decodeSchemaMetaDef(store *objstore.Store, id string) (enc.SchemaMeta, error) {
	data, err := getDef(store, id)
	if err != nil {
		return enc.SchemaMeta{}, err
	}
	return enc.DecodeSchemaMeta(data)
}

// renderSchemaMeta renders the DDL for a schema-meta op (roadmap 5.1b). Only the
// extension set produces DDL (CREATE EXTENSION IF NOT EXISTS, deterministic
// order); PGVersion and Groups are model-level metadata with no DDL surface. The
// statement is idempotent so re-applying the post-state is safe.
func renderSchemaMeta(meta enc.SchemaMeta) string {
	if len(meta.Extensions) == 0 {
		return "-- schema meta: no extension DDL"
	}
	exts := append([]string(nil), meta.Extensions...)
	sort.Strings(exts)
	lines := make([]string, len(exts))
	for i, e := range exts {
		lines[i] = sql.CreateExtension(e, true)
	}
	return strings.Join(lines, "\n")
}

func decodeCompositeDef(store *objstore.Store, id string) (model.CompositeType, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.CompositeType{}, err
	}
	return enc.DecodeCompositeType(data)
}

func decodeEnumClosure(store *objstore.Store, ids []string) ([]model.Enum, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]model.Enum, 0, len(ids))
	for _, id := range ids {
		data, err := getDef(store, id)
		if err != nil {
			return nil, err
		}
		e, err := enc.DecodeEnum(data)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func decodeDomainClosure(store *objstore.Store, ids []string) ([]model.Domain, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]model.Domain, 0, len(ids))
	for _, id := range ids {
		d, err := decodeDomainDef(store, id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func decodeTriggerDef(store *objstore.Store, id string) (model.Trigger, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.Trigger{}, err
	}
	var t model.Trigger
	if err := json.Unmarshal(data, &t); err != nil {
		return model.Trigger{}, fmt.Errorf("migrate: decode trigger def %s: %w", id, err)
	}
	return t, nil
}

func decodePolicyDef(store *objstore.Store, id string) (model.Policy, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.Policy{}, err
	}
	var p model.Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return model.Policy{}, fmt.Errorf("migrate: decode policy def %s: %w", id, err)
	}
	return p, nil
}

func decodePartitionSpec(store *objstore.Store, id string) (model.PartitionSpec, error) {
	data, err := getDef(store, id)
	if err != nil {
		return model.PartitionSpec{}, err
	}
	var s model.PartitionSpec
	if err := json.Unmarshal(data, &s); err != nil {
		return model.PartitionSpec{}, fmt.Errorf("migrate: decode partition spec %s: %w", id, err)
	}
	return s, nil
}

// getDef resolves a def object by content id, turning a missing object into a
// hard error (an op whose def does not resolve cannot render its true SQL).
func getDef(store *objstore.Store, id string) ([]byte, error) {
	data, err := store.Get(id)
	if err != nil {
		return nil, fmt.Errorf("migrate: op def %s does not resolve: %w", id, err)
	}
	return data, nil
}
