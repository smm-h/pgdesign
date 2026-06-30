package codegen

import (
	"strings"
	"testing"

	"github.com/smm-h/pgdesign/internal/model"
)

func TestSanitizeEnumValue_Go(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"active", "Active"},
		{"in-progress", "InProgress"},
		{"3rd-party", "_3rdParty"},
		{"ALREADY_CAPS", "AlreadyCaps"},
		{"multi--dash", "MultiDash"},
		{"with spaces", "WithSpaces"},
	}
	for _, tc := range cases {
		got := sanitizeEnumValue(tc.input, LangGo)
		if got != tc.want {
			t.Errorf("sanitizeEnumValue(%q, LangGo) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeEnumValue_Python(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"active", "ACTIVE"},
		{"in-progress", "IN_PROGRESS"},
		{"3rd-party", "_3RD_PARTY"},
		{"ALREADY_CAPS", "ALREADY_CAPS"},
		{"with spaces", "WITH_SPACES"},
	}
	for _, tc := range cases {
		got := sanitizeEnumValue(tc.input, LangPython)
		if got != tc.want {
			t.Errorf("sanitizeEnumValue(%q, LangPython) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeEnumValue_TS(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"in-progress", "in-progress"},
		{"3rd-party", "3rd-party"},
		{"anything goes!", "anything goes!"},
	}
	for _, tc := range cases {
		got := sanitizeEnumValue(tc.input, LangTS)
		if got != tc.want {
			t.Errorf("sanitizeEnumValue(%q, LangTS) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeEnumValue_Zig(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"active", "active"},
		{"in-progress", "in_progress"},
		{"ALREADY_CAPS", "already_caps"},
		{"3rd-party", "_3rd_party"},
	}
	for _, tc := range cases {
		got := sanitizeEnumValue(tc.input, LangZig)
		if got != tc.want {
			t.Errorf("sanitizeEnumValue(%q, LangZig) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeEnumValue_JavaKotlin(t *testing.T) {
	values := []string{"active", "in-progress", "3rd-party", "ALREADY_CAPS"}
	for _, v := range values {
		java := sanitizeEnumValue(v, LangJava)
		kotlin := sanitizeEnumValue(v, LangKotlin)
		if java != kotlin {
			t.Errorf("sanitizeEnumValue(%q): Java=%q != Kotlin=%q", v, java, kotlin)
		}
	}
}

func testEnum() model.Enum {
	return model.Enum{
		Name:    "status",
		Values:  []string{"active", "in-progress", "banned"},
		Comment: "user status",
	}
}

func TestGenerateGoEnum(t *testing.T) {
	out := GenerateEnums([]model.Enum{testEnum()}, LangGo)
	for _, want := range []string{
		"type Status string",
		"const (",
		`StatusActive Status = "active"`,
		`StatusInProgress Status = "in-progress"`,
		`StatusBanned Status = "banned"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Go enum output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestGenerateTSEnum(t *testing.T) {
	out := GenerateEnums([]model.Enum{testEnum()}, LangTS)
	want := `export type Status = "active" | "in-progress" | "banned";`
	if !strings.Contains(out, want) {
		t.Errorf("TS enum output missing %q\ngot:\n%s", want, out)
	}
}

func TestGeneratePythonEnum(t *testing.T) {
	out := GenerateEnums([]model.Enum{testEnum()}, LangPython)
	for _, want := range []string{
		"class Status(StrEnum):",
		`"""user status"""`,
		`ACTIVE = "active"`,
		`IN_PROGRESS = "in-progress"`,
		`BANNED = "banned"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Python enum output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestGenerateJavaEnum(t *testing.T) {
	out := GenerateEnums([]model.Enum{testEnum()}, LangJava)
	for _, want := range []string{
		"public enum Status {",
		`ACTIVE("active"),`,
		`IN_PROGRESS("in-progress"),`,
		`BANNED("banned");`,
		"private final String value;",
		"public String getValue()",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Java enum output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestGenerateKotlinEnum(t *testing.T) {
	out := GenerateEnums([]model.Enum{testEnum()}, LangKotlin)
	for _, want := range []string{
		"enum class Status(val value: String) {",
		`ACTIVE("active"),`,
		`IN_PROGRESS("in-progress"),`,
		`BANNED("banned");`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Kotlin enum output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestGenerateZigEnum(t *testing.T) {
	out := GenerateEnums([]model.Enum{testEnum()}, LangZig)
	for _, want := range []string{
		`pub const status_active = "active";`,
		`pub const status_in_progress = "in-progress";`,
		`pub const status_banned = "banned";`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Zig enum output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestGenerateEnums_Empty(t *testing.T) {
	got := GenerateEnums(nil, LangGo)
	if got != "" {
		t.Errorf("GenerateEnums(nil, LangGo) = %q, want empty string", got)
	}
}

func TestGenerateEnums_Multiple(t *testing.T) {
	enums := []model.Enum{
		{Name: "color", Values: []string{"red", "blue"}, Comment: "colors"},
		{Name: "size", Values: []string{"small", "large"}, Comment: "sizes"},
	}
	for _, lang := range []Lang{LangGo, LangTS, LangPython, LangJava, LangKotlin, LangZig} {
		out := GenerateEnums(enums, lang)
		if !strings.Contains(out, "red") || !strings.Contains(out, "small") {
			t.Errorf("GenerateEnums (lang=%s) missing enum values\ngot:\n%s", lang, out)
		}
	}
}

func TestSanitizeEnumValue_SpecialCharacters(t *testing.T) {
	// "foo@bar" splits into ["foo", "bar"]
	cases := []struct {
		input string
		lang  Lang
		want  string
	}{
		{"foo@bar", LangGo, "FooBar"},
		{"foo@bar", LangPython, "FOO_BAR"},
		{"foo@bar", LangZig, "foo_bar"},
		{"", LangGo, "_"},
		{"", LangPython, "_"},
		{"", LangZig, "_"},
		{"---", LangGo, "_"},
		{"---", LangPython, "_"},
		{"---", LangZig, "_"},
	}
	for _, tc := range cases {
		got := sanitizeEnumValue(tc.input, tc.lang)
		if got != tc.want {
			t.Errorf("sanitizeEnumValue(%q, %s) = %q, want %q", tc.input, tc.lang, got, tc.want)
		}
	}
}
