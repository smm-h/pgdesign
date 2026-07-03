package pgcap

import "sort"

// Capability identifies a PostgreSQL feature with a minimum version requirement.
type Capability int

const (
	IdentityColumns        Capability = iota // PG 10
	RestrictiveRLS                           // PG 10
	SequenceIfNotExists                      // PG 10
	MetadataOnlyDefault                      // PG 11
	AttGeneratedColumn                       // PG 12
	TransactionalEnumAdd                     // PG 12
	DropDBForce                              // PG 13
	CreateOrReplaceTrigger                   // PG 14
	CreateOrReplacePolicy                    // PG 15
	VirtualGeneratedCols                     // PG 18
)

// CapabilityInfo describes a PostgreSQL capability and the minimum version
// that introduced it.
type CapabilityInfo struct {
	Name        string
	MinVersion  int
	Description string
}

// registry maps each Capability to its metadata.
var registry = map[Capability]CapabilityInfo{
	IdentityColumns:        {Name: "IdentityColumns", MinVersion: 10, Description: "GENERATED AS IDENTITY columns"},
	RestrictiveRLS:         {Name: "RestrictiveRLS", MinVersion: 10, Description: "RESTRICTIVE row-level security policies"},
	SequenceIfNotExists:    {Name: "SequenceIfNotExists", MinVersion: 10, Description: "CREATE SEQUENCE IF NOT EXISTS"},
	MetadataOnlyDefault:    {Name: "MetadataOnlyDefault", MinVersion: 11, Description: "ADD COLUMN with non-null default is metadata-only"},
	AttGeneratedColumn:     {Name: "AttGeneratedColumn", MinVersion: 12, Description: "pg_attribute.attgenerated column"},
	TransactionalEnumAdd:   {Name: "TransactionalEnumAdd", MinVersion: 12, Description: "ALTER TYPE ADD VALUE inside transactions"},
	DropDBForce:            {Name: "DropDBForce", MinVersion: 13, Description: "DROP DATABASE WITH (FORCE)"},
	CreateOrReplaceTrigger: {Name: "CreateOrReplaceTrigger", MinVersion: 14, Description: "CREATE OR REPLACE TRIGGER"},
	CreateOrReplacePolicy:  {Name: "CreateOrReplacePolicy", MinVersion: 15, Description: "CREATE OR REPLACE POLICY"},
	VirtualGeneratedCols:   {Name: "VirtualGeneratedCols", MinVersion: 18, Description: "VIRTUAL generated columns"},
}

// Has reports whether the given PostgreSQL major version supports the
// capability. Returns false when pgVersion is 0 (unknown version).
func Has(pgVersion int, cap Capability) bool {
	if pgVersion == 0 {
		return false
	}
	info, ok := registry[cap]
	if !ok {
		return false
	}
	return pgVersion >= info.MinVersion
}

// MinVersion returns the minimum PostgreSQL major version that introduced
// the capability. Returns 0 if the capability is unknown.
func MinVersion(cap Capability) int {
	info, ok := registry[cap]
	if !ok {
		return 0
	}
	return info.MinVersion
}

// All returns every registered capability sorted by minimum version
// (ascending), with ties broken by name.
func All() []CapabilityInfo {
	result := make([]CapabilityInfo, 0, len(registry))
	for _, info := range registry {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].MinVersion != result[j].MinVersion {
			return result[i].MinVersion < result[j].MinVersion
		}
		return result[i].Name < result[j].Name
	})
	return result
}
