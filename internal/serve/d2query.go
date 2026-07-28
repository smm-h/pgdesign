package serve

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/smm-h/pgdesign/internal/generate"
)

// d2OptionsFromQuery builds generate.D2Options from the request query string,
// starting from the intended defaults and overriding each option the caller
// specified. It maps the same knobs the [output.<name>.d2] config subsection
// exposes, so the serve d2/svg endpoints and `build` produce equivalent output
// for equivalent settings. It returns a validation error for any bad value.
//
// Query parameters:
//   - layout=dagre|elk, theme=<int>, direction=down|right|left|up
//   - index_markers, nullable, comments, checks, rls_markers, enums,
//     cardinality, summary = true|false (bools; default true except summary)
//   - include, exclude = comma-separated glob patterns
//   - include_dependencies = <int depth>
//   - heat_map = fan-in|fan-out
func d2OptionsFromQuery(r *http.Request) (generate.D2Options, error) {
	q := r.URL.Query()
	opts := generate.DefaultD2Options()

	if v := q.Get("layout"); v != "" {
		opts.Layout = v
	}
	if v := q.Get("direction"); v != "" {
		opts.Direction = v
	}
	if v := q.Get("theme"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return opts, &badParamError{param: "theme", value: v}
		}
		opts.Theme = n
	}

	boolParam := func(name string, target *bool) error {
		v := q.Get(name)
		if v == "" {
			return nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return &badParamError{param: name, value: v}
		}
		*target = b
		return nil
	}
	for _, bp := range []struct {
		name   string
		target *bool
	}{
		{"index_markers", &opts.IndexMarkers},
		{"nullable", &opts.Nullable},
		{"comments", &opts.Comments},
		{"checks", &opts.Checks},
		{"rls_markers", &opts.RLSMarkers},
		{"enums", &opts.Enums},
		{"cardinality", &opts.Cardinality},
		{"summary", &opts.Summary},
	} {
		if err := boolParam(bp.name, bp.target); err != nil {
			return opts, err
		}
	}

	if v := q.Get("include"); v != "" {
		opts.Include = splitCSV(v)
	}
	if v := q.Get("exclude"); v != "" {
		opts.Exclude = splitCSV(v)
	}
	if v := q.Get("include_dependencies"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return opts, &badParamError{param: "include_dependencies", value: v}
		}
		opts.IncludeDependencies = n
	}
	if v := q.Get("heat_map"); v != "" {
		opts.HeatMap = v
	}

	if err := opts.Validate(); err != nil {
		return opts, err
	}
	return opts, nil
}

// splitCSV splits a comma-separated list, trimming whitespace and dropping
// empty entries.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// badParamError reports an invalid query parameter value.
type badParamError struct {
	param string
	value string
}

func (e *badParamError) Error() string {
	return "invalid query parameter " + e.param + "=" + e.value
}
