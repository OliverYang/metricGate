package engine

import (
	"testing"
)

// Each case is one language, one policy ban, and one source file that must
// trip it. The point is the YAML staying live across languages: a ban that
// silently does nothing in seven of nine languages reads exactly like
// compliant code, which is the worst failure a gate can have.
func TestPolicyBanCoversEveryLanguage(t *testing.T) {
	cases := []struct {
		name   string
		lang   string
		policy string
		file   string
		src    string
		want   string
	}{{
		name: "typescript call", lang: "typescript", want: "banned-console-log",
		policy: "console.log", file: "a.ts",
		src: "export function go(): void {\n  console.log(\"hi\");\n}\n",
	}, {
		name: "javascript call", lang: "javascript", want: "banned-console-log",
		policy: "console.log", file: "b.js",
		src: "function go() { console.log(\"hi\"); }\n",
	}, {
		name: "python call", lang: "python", want: "banned-requests-get",
		policy: "requests.get", file: "c.py",
		src: "import requests\ndef go():\n    requests.get(\"http://x\")\n",
	}, {
		name: "c call", lang: "c", want: "banned-strcpy",
		policy: "strcpy", file: "d.c",
		src: "#include <string.h>\nvoid go(char *d, char *s) { strcpy(d, s); }\n",
	}, {
		name: "cpp qualified call", lang: "cpp", want: "banned-std::printf",
		policy: "std::printf", file: "e.cpp",
		src: "#include <cstdio>\nvoid go() { std::printf(\"hi\"); }\n",
	}, {
		name: "csharp call", lang: "csharp", want: "banned-Console-WriteLine",
		policy: "Console.WriteLine", file: "f.cs",
		src: "class A {\n  void Go() { Console.WriteLine(\"hi\"); }\n}\n",
	}, {
		name: "rust qualified call", lang: "rust", want: "banned-fs::read_to_string",
		policy: "fs::read_to_string", file: "g.rs",
		src: "use std::fs;\nfn go() { let s = fs::read_to_string(\"x\"); }\n",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newPolicyEngine(t, banPolicy(tc.lang, "call", tc.policy))
			findings, err := eng.CheckSource(tc.file, []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if got := countRules(findings)[tc.want]; got != 1 {
				t.Fatalf("rule %s fired %d times, want 1 (findings: %v)", tc.want, got, findings)
			}
		})
	}
}

// Import bans have to survive each language's own import syntax: quotes,
// angle brackets, dotted names and Rust's "crate::" root.
func TestPolicyImportBanCoversEveryLanguage(t *testing.T) {
	cases := []struct {
		name, lang, policy, file, src, want string
	}{{
		name: "typescript", lang: "typescript", policy: "left-pad", file: "a.ts",
		src:  "import x from \"left-pad\";\n",
		want: "banned-left-pad",
	}, {
		name: "python", lang: "python", policy: "requests", file: "b.py",
		src:  "import requests\n",
		want: "banned-requests",
	}, {
		name: "c angle include", lang: "c", policy: "openssl/ssl.h", file: "c.c",
		src:  "#include <openssl/ssl.h>\n",
		want: "banned-openssl-ssl-h",
	}, {
		name: "cpp angle include", lang: "cpp", policy: "boost/asio.hpp", file: "d.cpp",
		src:  "#include <boost/asio.hpp>\n",
		want: "banned-boost-asio-hpp",
	}, {
		name: "csharp using", lang: "csharp", policy: "Newtonsoft.Json", file: "e.cs",
		src:  "using Newtonsoft.Json;\n",
		want: "banned-Newtonsoft-Json",
	}, {
		name: "rust use", lang: "rust", policy: "openssl", file: "f.rs",
		src:  "use openssl::ssl;\n",
		want: "banned-openssl",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newPolicyEngine(t, banPolicy(tc.lang, "import", tc.policy))
			findings, err := eng.CheckSource(tc.file, []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if got := countRules(findings)[tc.want]; got != 1 {
				t.Fatalf("rule %s fired %d times, want 1 (findings: %v)", tc.want, got, findings)
			}
		})
	}
}

// A Rust "use" of the file's own crate or of a parent module is not a
// dependency on a package called "crate" or "super", so it must not match a
// ban spelled that way — and the ban must still fire on a real crate.
func TestRustImportBanIgnoresLocalRoots(t *testing.T) {
	eng := newPolicyEngine(t, banPolicy("rust", "import", "crate"))
	findings, err := eng.CheckSource("a.rs", []byte("use crate::geo::Shape;\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none (crate:: is the local crate)", findings)
	}
}

// Constructor bans are a separate shape from call bans (new X(...) is not a
// method invocation), so they need their own coverage outside Go/Java.
func TestPolicyConstructorBanCoversEveryLanguage(t *testing.T) {
	cases := []struct {
		name, lang, policy, file, src, want string
	}{{
		name: "typescript", lang: "typescript", policy: "Foo", file: "a.ts",
		src:  "const f = new Foo(1);\n",
		want: "banned-Foo",
	}, {
		name: "cpp", lang: "cpp", policy: "Foo", file: "b.cpp",
		src:  "void go() { auto f = new Foo(1); }\n",
		want: "banned-Foo",
	}, {
		name: "csharp", lang: "csharp", policy: "HttpClient", file: "c.cs",
		src:  "class A { void Go() { var c = new HttpClient(); } }\n",
		want: "banned-HttpClient",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newPolicyEngine(t, banPolicy(tc.lang, "constructor", tc.policy))
			findings, err := eng.CheckSource(tc.file, []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if got := countRules(findings)[tc.want]; got != 1 {
				t.Fatalf("rule %s fired %d times, want 1 (findings: %v)", tc.want, got, findings)
			}
		})
	}
}

// banPolicy renders a one-ban policy for a language.
func banPolicy(lang, kind, match string) string {
	return "version: 1\nlanguages:\n  " + lang + ":\n    banned-api:\n" +
		"      - kind: " + kind + "\n        match: \"" + match + "\"\n        reason: banned\n"
}
