package enc

import (
	"encoding/json"
	"fmt"

	"github.com/smm-h/pgdesign/internal/model"
)

// DecodeTable decodes canonical bytes into a table.
func DecodeTable(data []byte) (model.Table, error) {
	var f tableForm
	if err := json.Unmarshal(data, &f); err != nil {
		return model.Table{}, fmt.Errorf("enc: decode table: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindTable); err != nil {
		return model.Table{}, err
	}
	return tableFromForm(f), nil
}

// DecodeView decodes canonical bytes into a view.
func DecodeView(data []byte) (model.View, error) {
	var f viewForm
	if err := json.Unmarshal(data, &f); err != nil {
		return model.View{}, fmt.Errorf("enc: decode view: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindView); err != nil {
		return model.View{}, err
	}
	return viewFromForm(f), nil
}

// DecodeMaterializedView decodes canonical bytes into a materialized view.
func DecodeMaterializedView(data []byte) (model.MaterializedView, error) {
	var f matViewForm
	if err := json.Unmarshal(data, &f); err != nil {
		return model.MaterializedView{}, fmt.Errorf("enc: decode matview: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindMatView); err != nil {
		return model.MaterializedView{}, err
	}
	return matViewFromForm(f), nil
}

// DecodeSequence decodes canonical bytes into a sequence.
func DecodeSequence(data []byte) (model.Sequence, error) {
	var f sequenceForm
	if err := json.Unmarshal(data, &f); err != nil {
		return model.Sequence{}, fmt.Errorf("enc: decode sequence: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindSequence); err != nil {
		return model.Sequence{}, err
	}
	return sequenceFromForm(f), nil
}

// DecodeFunction decodes canonical bytes into a function.
func DecodeFunction(data []byte) (model.Function, error) {
	var f functionForm
	if err := json.Unmarshal(data, &f); err != nil {
		return model.Function{}, fmt.Errorf("enc: decode function: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindFunction); err != nil {
		return model.Function{}, err
	}
	return functionFromForm(f), nil
}

// DecodeEnum decodes canonical bytes into an enum type.
func DecodeEnum(data []byte) (model.Enum, error) {
	var f enumForm
	if err := json.Unmarshal(data, &f); err != nil {
		return model.Enum{}, fmt.Errorf("enc: decode enum: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindEnum); err != nil {
		return model.Enum{}, err
	}
	return enumFromForm(f), nil
}

// DecodeDomain decodes canonical bytes into a domain type.
func DecodeDomain(data []byte) (model.Domain, error) {
	var f domainForm
	if err := json.Unmarshal(data, &f); err != nil {
		return model.Domain{}, fmt.Errorf("enc: decode domain: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindDomain); err != nil {
		return model.Domain{}, err
	}
	return domainFromForm(f), nil
}

// DecodeCompositeType decodes canonical bytes into a composite type.
func DecodeCompositeType(data []byte) (model.CompositeType, error) {
	var f compositeForm
	if err := json.Unmarshal(data, &f); err != nil {
		return model.CompositeType{}, fmt.Errorf("enc: decode composite: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindComposite); err != nil {
		return model.CompositeType{}, err
	}
	return compositeFromForm(f), nil
}

// SchemaMeta is the decoded schema-global header. It is a distinct type from
// model.Schema because a header carries only the schema-global fields, not the
// per-object collections.
type SchemaMeta struct {
	Name       string
	Extensions []string
	Groups     map[string][]string
	PGVersion  int
}

// DecodeSchemaMeta decodes canonical bytes into the schema-global header.
func DecodeSchemaMeta(data []byte) (SchemaMeta, error) {
	var f schemaMetaForm
	if err := json.Unmarshal(data, &f); err != nil {
		return SchemaMeta{}, fmt.Errorf("enc: decode schema meta: %w", err)
	}
	if err := checkHeader(f.Codec, f.Kind, KindSchemaMeta); err != nil {
		return SchemaMeta{}, err
	}
	return SchemaMeta{Name: f.Name, Extensions: f.Extensions, Groups: f.Groups, PGVersion: f.PGVersion}, nil
}

// checkHeader validates the codec epoch and self-describing kind on a decoded
// form.
func checkHeader(codec int, got, want Kind) error {
	if codec != CodecVersion {
		return fmt.Errorf("enc: decode %s: codec epoch %d, want %d", want, codec, CodecVersion)
	}
	if got != want {
		return fmt.Errorf("enc: decode: kind %q, want %q", got, want)
	}
	return nil
}
