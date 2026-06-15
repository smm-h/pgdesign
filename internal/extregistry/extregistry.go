// Package extregistry implements the PostgreSQL extension capability registry.
// It maps extension names to what they provide (types, opclasses, functions,
// index methods) and supports reverse lookups.
package extregistry

// Extension describes what a PostgreSQL extension provides.
type Extension struct {
	Name         string
	Types        []string
	Opclasses    []string
	Functions    []string
	IndexMethods []string
}

// Registry holds extensions and reverse-lookup maps.
type Registry struct {
	extensions map[string]*Extension
	opclassMap map[string]string // opclass name -> extension name
	typeMap    map[string]string // type name -> extension name
	funcMap        map[string]string // function name -> extension name
	indexMethodMap map[string]string // index method name -> extension name
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		extensions: make(map[string]*Extension),
		opclassMap: make(map[string]string),
		typeMap:    make(map[string]string),
		funcMap:        make(map[string]string),
		indexMethodMap: make(map[string]string),
	}
}

// Register adds an extension to the registry and builds reverse maps.
func (r *Registry) Register(ext *Extension) {
	r.extensions[ext.Name] = ext
	for _, oc := range ext.Opclasses {
		r.opclassMap[oc] = ext.Name
	}
	for _, t := range ext.Types {
		r.typeMap[t] = ext.Name
	}
	for _, f := range ext.Functions {
		r.funcMap[f] = ext.Name
	}
	for _, m := range ext.IndexMethods {
		r.indexMethodMap[m] = ext.Name
	}
}

// RequiredExtension returns which extension provides the given opclass.
func (r *Registry) RequiredExtension(opclass string) (string, bool) {
	name, ok := r.opclassMap[opclass]
	return name, ok
}

// RequiredExtensionForType returns which extension provides the given type.
func (r *Registry) RequiredExtensionForType(typeName string) (string, bool) {
	name, ok := r.typeMap[typeName]
	return name, ok
}

// RequiredExtensionForFunction returns which extension provides the given function.
func (r *Registry) RequiredExtensionForFunction(funcName string) (string, bool) {
	name, ok := r.funcMap[funcName]
	return name, ok
}

// RequiredExtensionForMethod returns which extension provides the given index method.
func (r *Registry) RequiredExtensionForMethod(method string) (string, bool) {
	name, ok := r.indexMethodMap[method]
	return name, ok
}

// UserExtension represents a user-defined extension from pgdesign.toml config.
type UserExtension struct {
	Name         string
	Types        []string
	Opclasses    []string
	Functions    []string
	IndexMethods []string
}

// LoadUserExtensions adds user-defined extensions to the registry.
func (r *Registry) LoadUserExtensions(exts []UserExtension) {
	for i := range exts {
		r.Register(&Extension{
			Name:         exts[i].Name,
			Types:        exts[i].Types,
			Opclasses:    exts[i].Opclasses,
			Functions:    exts[i].Functions,
			IndexMethods: exts[i].IndexMethods,
		})
	}
}
