package codescan

import (
	"fmt"
	"strings"
	"testing"
)

const cppSample = `// ==WindhawkMod==
// @name Sample
#include <windows.h>
#include "util.h"
#define MAX_ROWS 64
#define CLAMP(x) ((x) < 0 ? 0 : (x))

const char* kSettings = R"(
{ "not": { "a": "block" } }
)";

struct Row;
struct Row {
    int image;
    bool IsSep() const { return image < 0; }
};

enum class Mode : int {
    Fast,
    Slow,
};

typedef struct _POINTF {
    float x, y;
} POINTF;

using Rows = std::vector<Row>;

class Overlay final : public Base {
public:
    explicit Overlay(HWND parent);
    virtual ~Overlay() override;
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** out) override {
        return Helper(riid, out);
    }
    void Invalidate()
    {
        if (dirty) {
            Redraw(rows);
        }
        int n = Count(rows);
        Foo bar(n);
    }
};

Overlay::Overlay(HWND parent)
    : parent_(parent), count_(Count(rows)) {
    Attach(parent);
}

static LRESULT CALLBACK WndProc(HWND hwnd, UINT msg,
                                WPARAM wp, LPARAM lp) {
    switch (msg) {
    case WM_PAINT: {
        Paint(hwnd);
        return 0;
    }
    }
    return DefWindowProcW(hwnd, msg, wp, lp);
}

int Count(const Rows& rows);

void* __stdcall Alloc(size_t n) { return malloc(n); }

BOOL Wh_ModInit() {
    return Setup() && CLAMP(1);
}
`

func TestScanCpp(t *testing.T) {
	decls, refs := Scan([]byte(cppSample), LangFor("x.wh.cpp"))
	var got []string
	for _, d := range decls {
		got = append(got, fmt.Sprintf("%d-%d %s %s", d.Line, d.EndLine, d.Kind, d.Name))
	}
	want := []string{
		"5-5 macro MAX_ROWS",
		"6-6 macro CLAMP",
		"13-16 struct Row",
		"15-15 method IsSep",
		"18-21 enum Mode",
		"23-25 struct _POINTF",
		"27-27 type Rows",
		"29-44 class Overlay",
		"33-35 method QueryInterface",
		"36-43 method Invalidate",
		"46-49 method Overlay",
		"51-60 func WndProc",
		"64-64 func Alloc",
		"66-68 func Wh_ModInit",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("decls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, d := range decls {
		if (d.Name == "WndProc") == d.Exported {
			t.Errorf("only the static WndProc should be unexported: %+v", d)
		}
	}

	calls := map[string]bool{}
	imports := map[string]bool{}
	for _, r := range refs {
		switch r.Rel {
		case "calls":
			calls[r.Target] = true
		case "imports":
			imports[r.Target] = true
		}
	}
	for _, c := range []string{"Helper", "Redraw", "Count", "Attach", "Paint", "DefWindowProcW", "Setup", "malloc"} {
		if !calls[c] {
			t.Errorf("missing call %s", c)
		}
	}
	for _, c := range []string{"if", "switch", "block", "a"} {
		if calls[c] {
			t.Errorf("false call %s", c)
		}
	}
	if !imports["windows.h"] || !imports["util.h"] {
		t.Errorf("imports: %v", imports)
	}
}

func TestRawStringPrefixes(t *testing.T) {
	src := "auto a = LR\"x({ \"q\" })x\"; auto b = u8R\"({)\"; int fooR = 1; void F() {\n}\n"
	clean, _ := cleanLines(strings.Split(src, "\n"), langC)
	if strings.Contains(clean[0], "q") || strings.Count(clean[0], "{") != 1 {
		t.Errorf("raw strings not blanked: %q", clean[0])
	}
}
