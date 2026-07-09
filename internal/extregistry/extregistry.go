// Package extregistry implements the PostgreSQL extension capability registry, mapping extension names to the types, opclasses, and functions they provide.
package extregistry

// Extension describes what a PostgreSQL extension provides.
type Extension struct {
	Name         string
	DDLName      string // PostgreSQL CREATE EXTENSION name, if different from Name (e.g. "vector" for pgvector)
	Types        []string
	Opclasses    []string
	Functions    []string
	IndexMethods []string
	IndexParams  map[string][]string // index method -> valid parameter names
}

// Registry holds extensions and reverse-lookup maps.
type Registry struct {
	extensions     map[string]*Extension
	opclassMap     map[string]string   // opclass name -> extension name
	typeMap        map[string]string   // type name -> extension name
	funcMap        map[string]string   // function name -> extension name
	indexMethodMap map[string]string   // index method name -> extension name
	indexParamMap  map[string][]string // method name -> valid param names (aggregated from all extensions)
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		extensions:     make(map[string]*Extension),
		opclassMap:     make(map[string]string),
		typeMap:        make(map[string]string),
		funcMap:        make(map[string]string),
		indexMethodMap: make(map[string]string),
		indexParamMap:  make(map[string][]string),
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
	for method, params := range ext.IndexParams {
		r.indexParamMap[method] = append(r.indexParamMap[method], params...)
	}
}

// ResolveDDLName returns the PostgreSQL extension name to use in CREATE EXTENSION
// statements. Three-tier fallback:
//   - If extension found and ext.DDLName non-empty, return ext.DDLName
//   - If extension found and ext.DDLName empty, return ext.Name
//   - If extension not found (user-defined), return input name as-is
func (r *Registry) ResolveDDLName(name string) string {
	ext, ok := r.extensions[name]
	if !ok {
		return name
	}
	if ext.DDLName != "" {
		return ext.DDLName
	}
	return ext.Name
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

// ValidIndexParams returns the valid WITH parameter names for an index method.
// Returns (nil, false) if the method is not registered.
func (r *Registry) ValidIndexParams(method string) ([]string, bool) {
	params, ok := r.indexParamMap[method]
	return params, ok
}

// UserExtension represents a user-defined extension from pgdesign.toml config.
type UserExtension struct {
	Name         string
	Types        []string
	Opclasses    []string
	Functions    []string
	IndexMethods []string
	IndexParams  map[string][]string
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
			IndexParams:  exts[i].IndexParams,
		})
	}
}
