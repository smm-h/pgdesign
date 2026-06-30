package main

import (
	"github.com/smm-h/pgdesign/internal/config"
	"github.com/smm-h/pgdesign/internal/diagnostic"
	"github.com/smm-h/pgdesign/internal/extregistry"
	"github.com/smm-h/pgdesign/internal/model"
	"github.com/smm-h/pgdesign/internal/semtype"
	"github.com/smm-h/pgdesign/internal/validate"
)

// validateSchema runs validation on a parsed and built schema using the
// project configuration. It creates the extension registry, constructs the
// validate.Config, and calls validate.Validate. Returns the diagnostics
// and any error from validate itself.
func validateSchema[P config.PathKind](schema *model.Schema, typeReg *semtype.Registry, cfg *config.Config[P], pgVersion int) []diagnostic.Diagnostic {
	extReg := extregistry.NewBuiltinRegistry()
	extReg.LoadUserExtensions(configToUserExtensions(cfg.Extensions))

	valCfg := &validate.Config{
		NamingPattern: cfg.Validate.NamingPattern,
		MaxColumns:    cfg.Validate.MaxColumns,
		Disabled:      cfg.Validate.Disable,
		Suppress:      cfg.Suppress,
		Extensions:    schema.Extensions,
		ExtRegistry:   extReg,
		TypeRegistry:  typeReg,
	}

	diags, _ := validate.Validate(schema, valCfg)
	return diags
}
