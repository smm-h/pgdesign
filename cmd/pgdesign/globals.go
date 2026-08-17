package main

import "github.com/smm-h/strictcli/go/strictcli"

// registerGlobals adds app-wide global flags. Handlers read them from kwargs:
//
//	project_config -> kwargs["project_config"] (nil when not provided, string when set)
//
// --quiet is NOT here: it is one of strictcli's reserved quartet
// (--dry-run, --approve-consequential, --quiet, --verbose), owned by the
// framework and delivered on the Context. Handlers read ctx.Quiet(); declaring
// it as a flag at any level is a registration-time hard error.
func registerGlobals(app *strictcli.App) {
	app.GlobalFlag(strictcli.StringFlag("project-config", "Path to pgdesign.toml (bypasses directory search)", strictcli.Optional()))
}

// optBool, optStr and optInt resolve an optional flag's absence to the fallback
// its own help text declares.
//
// strictcli's mutating-default ban (contract §27.1) forbids Default() on any
// flag or positional arg of a command declaring effect="mutating": absence must
// never resolve to a value the invocation did not state, because on a mutating
// command a value the framework picked is a value the framework writes. Every
// pgdesign switch that used to carry a Default() on a mutating command now
// declares Optional() and NAMES its fallback in its own help text, and these
// three functions are the only place where absence becomes that fallback — so
// no code further down ever receives a nil it would misread as a zero value.
func optBool(v interface{}, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return v.(bool)
}

func optStr(v interface{}, fallback string) string {
	if v == nil {
		return fallback
	}
	return v.(string)
}

func optInt(v interface{}, fallback int) int {
	if v == nil {
		return fallback
	}
	return v.(int)
}

// firstNonEmpty returns the first non-empty string, or "" when there is none.
// It composes with optStr where a flag's documented fallback is itself layered
// (an absent flag reads the project config, and an absent config entry reads a
// literal).
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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

// kwargsDBURL extracts the resolved value of a ConnectionURLFlag-bound --db
// flag. The flag declares Optional() with a PGDESIGN_DB env
// fallback handled by the framework (cli > env, hermetic-suppressed); this
// returns "" when neither the flag nor the env supplied a value, so a command
// that requires a database can enforce it loudly at the handler.
func kwargsDBURL(kwargs map[string]interface{}) string {
	if v := kwargsOptString(kwargs, "db"); v != nil {
		return *v
	}
	return ""
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
// Optional()+was-set pattern (the `output` flag is the blueprint). dirFlag is
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
