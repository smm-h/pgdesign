package parse

import "github.com/smm-h/pgdesign/internal/semtype"

// CollectUserTypes converts a RawSchema's user-defined types into UserTypeDefs
// suitable for loading into a semtype.Registry. This is the single canonical
// conversion point — all callers should use this instead of inline conversion.
func CollectUserTypes(raw *RawSchema) []semtype.UserTypeDef {
	var userTypes []semtype.UserTypeDef
	for _, rt := range raw.Types {
		ut := semtype.UserTypeDef{
			Name:   rt.Name,
			Kind:   rt.Kind,
			Base:   rt.BaseType,
			Values: rt.Values,
			Fields: rt.Fields,
		}
		if rt.Extends != nil {
			ut.Extends = *rt.Extends
		}
		if rt.NotNull != nil {
			ut.NotNull = rt.NotNull
		}
		if rt.Default != nil {
			v := *rt.Default
			ut.Default = &v
		}
		if rt.DefaultExpr != nil {
			ut.DefaultExpr = *rt.DefaultExpr
		}
		if rt.Check != nil {
			ut.Check = *rt.Check
		}
		if rt.Unique != nil {
			ut.Unique = *rt.Unique
		}
		if rt.Array != nil {
			ut.Array = *rt.Array
		}
		if rt.Comment != nil {
			ut.Comment = *rt.Comment
		}
		// State machine fields
		if rt.InitialState != nil {
			ut.InitialState = *rt.InitialState
		}
		ut.EnforceTrigger = rt.EnforceTrigger
		for _, s := range rt.States {
			us := semtype.UserSMState{Name: s.Name}
			if s.Terminal != nil {
				us.Terminal = *s.Terminal
			}
			if s.Comment != nil {
				us.Comment = *s.Comment
			}
			ut.States = append(ut.States, us)
		}
		for _, tr := range rt.Transitions {
			utTr := semtype.UserSMTransition{
				Name: tr.Name,
				From: tr.From,
				To:   tr.To,
			}
			if tr.Requires != nil {
				utTr.Requires = tr.Requires
			}
			if tr.Comment != nil {
				utTr.Comment = *tr.Comment
			}
			ut.Transitions = append(ut.Transitions, utTr)
		}
		userTypes = append(userTypes, ut)
	}
	return userTypes
}
