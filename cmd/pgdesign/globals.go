package main

import "github.com/smm-h/strictcli/go/strictcli"

// registerGlobals adds app-wide global flags. Handlers read them from kwargs:
//
//	quiet          -> kwargs["quiet"].(bool)
//	project_config -> kwargs["project_config"] (nil when not provided, string when set)
func registerGlobals(app *strictcli.App) {
	app.GlobalFlag(strictcli.BoolFlag("quiet", "Suppress non-error output", strictcli.Default(false)))
	app.GlobalFlag(strictcli.StringFlag("project-config", "Path to pgdesign.toml (bypasses directory search)", strictcli.Default(nil)))
}

// kwargsQuiet extracts the quiet global flag from kwargs.
func kwargsQuiet(kwargs map[string]interface{}) bool {
	return kwargs["quiet"].(bool)
}

// kwargsConfigOverride extracts the project-config global flag from kwargs.
// Returns nil when not provided.
func kwargsConfigOverride(kwargs map[string]interface{}) *string {
	v := kwargs["project_config"]
	if v == nil {
		return nil
	}
	s := v.(string)
	return &s
}

// kwargsStrSlice converts a variadic arg or list flag to []string. It tolerates
// both the []interface{} form (variadic positional args) and the []string form
// (strictcli list flags).
func kwargsStrSlice(v interface{}) []string {
	switch raw := v.(type) {
	case nil:
		return nil
	case []string:
		return raw
	case []interface{}:
		out := make([]string, len(raw))
		for i, r := range raw {
			out[i] = r.(string)
		}
		return out
	default:
		return nil
	}
}

// kwargsOptString extracts an optional string flag. Returns nil when not provided.
func kwargsOptString(kwargs map[string]interface{}, key string) *string {
	v := kwargs[key]
	if v == nil {
		return nil
	}
	s := v.(string)
	return &s
}

// kwargsOptInt extracts an optional int flag. Returns nil when not provided.
func kwargsOptInt(kwargs map[string]interface{}, key string) *int {
	v := kwargs[key]
	if v == nil {
		return nil
	}
	i := v.(int)
	return &i
}

// resolveMigrationsDir resolves the effective migrations directory using the
// Default(nil)+was-set pattern (the `output` flag is the blueprint). dirFlag is
// the raw --dir flag value: nil when the user did not pass --dir, a pointer to
// the exact string when they did. An explicit flag ALWAYS wins verbatim -- even
// "--dir migrations" -- so it is distinguishable from the unset default. Only
// when --dir is absent does the project config's migrations_dir apply, falling
// back to the literal "migrations" when neither is set.
func resolveMigrationsDir(dirFlag *string, cfgDir string) string {
	if dirFlag != nil {
		return *dirFlag
	}
	if cfgDir != "" {
		return cfgDir
	}
	return "migrations"
}

// toIfaces converts []string to []interface{} for strictcli.Choices().
func toIfaces(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
