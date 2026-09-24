package jsrun

import "testing"

// Numbers print as JavaScript's Number.prototype.toString prints them.
func TestJSNumberWritesNumbersAsJavaScriptDoes(t *testing.T) {
	for value, want := range map[float64]string{
		0: "0", 1: "1", 16: "16", 1.5: "1.5", 0.5: "0.5", 1000: "1000", -1: "-1",
		1e21: "1e+21", 1e20: "100000000000000000000", 123456789012345680000: "123456789012345680000",
		0.000001: "0.000001", 1e-7: "1e-7", 1.5e-7: "1.5e-7", 2.5e25: "2.5e+25", 0.30000000000000004: "0.30000000000000004",
	} {
		if got := jsNumber(value); got != want {
			t.Errorf("jsNumber(%v) = %q, want %q", value, got, want)
		}
	}
}

// Only the user's own innermost frame locates a call; an error a built-in
// threw from inside the call is not the call's.
func TestV8WordingNamesACalleeOnlyFromTheCodesOwnFrame(t *testing.T) {
	sites := map[position]callSite{{2, 20}: {text: "a.map"}, {3, 5}: {text: "X", construct: true}}
	for _, test := range []struct{ message, frames, want string }{
		{"Object has no member 'map'", "\n\tat Code:2:20(8)\n\tat call (native)\n", "a.map is not a function"},
		{"Object has no member 'map'", "\n\tat inner (Code:2:20(8))\n", "a.map is not a function"},
		{"Value is not an object: 1", "\n\tat apply (native)\n\tat Code:2:20(8)\n", ""},
		{"Object has no member 'map'", "\n\tat Code:2:21(8)\n", ""},
		{"Value is not a constructor", "\n\tat Code:3:5(2)\n", "X is not a constructor"},
		{"Value is not a constructor", "\n\tat Code:2:20(2)\n", ""},
		{"Object has no member 'X'", "\n\tat Code:3:5(2)\n", ""},
		{"Cannot read property 'x' of undefined or null", "", "Cannot read properties of undefined (reading 'x')"},
		{"Value is not object coercible", "\n\tat Code:2:20(8)\n", ""},
	} {
		if got := v8Wording(test.message, test.frames, sites); got != test.want {
			t.Errorf("v8Wording(%q, %q) = %q, want %q", test.message, test.frames, got, test.want)
		}
	}
}
