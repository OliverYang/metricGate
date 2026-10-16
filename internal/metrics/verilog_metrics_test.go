package metrics

import (
	"testing"

	"metricgate/internal/parser"
)

// SystemVerilog has no fields on any declaration or statement node, so every
// number below is produced by the adapter reading child kinds and anonymous
// token text. These tests pin the decisions that are easy to get wrong: the
// `else` branch of an if, the default arm of a case, the loop header, and the
// fact that a class constructor's statements hang directly off the
// declaration rather than off a *_body_declaration.

// One function with every construct the adapter claims to lower:
//
//	cyclomatic = 1 (base)
//	           + 1 if + 1 else-if          (conditional_statement x2)
//	           + 2 case arms               (case_item x3, but `default` is not a
//	                                        decision; `2, 3:` is one arm)
//	           + 1 for + 1 while + 1 repeat
//	           + 1 ternary                 (conditional_expression)
//	           + 2 && operands             (a 3-way run joins 3 paths)
//	           = 11
//
//	cognitive = if(1) + else-if(1, same nesting) + case(1)
//	          + for(1) + while(1) + repeat(1) + ternary(1) + &&(1) = 8
//	maxNesting = 2 (the if's body holds the case, whose arms sit at 2)
const verilogAllConstructsSrc = `module m;
  function automatic int f(int a, int b, int c);
    int r;
    if (a > 0) begin
      r = 1;
    end else if (b > 0) begin
      r = 2;
    end else begin
      r = 3;
    end
    case (a)
      1: r = 4;
      2, 3: r = 5;
      default: r = 6;
    endcase
    for (int i = 0; i < 10; i = i + 1) begin
      r = r + i;
    end
    while (r < 100) begin
      r = r * 2;
    end
    repeat (3) r = r + 1;
    r = (a > 0) ? r : -r;
    r = (a > 0) && (b > 0) && (c > 0);
    return r;
  endfunction
endmodule`

func TestVerilogFunctionMetrics(t *testing.T) {
	fs := analyze(t, parser.LangVerilog, verilogAllConstructsSrc)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	fn := fs.Functions[0]
	if fn.Name != "f" {
		t.Errorf("name = %q, want f (the grammar wraps the name in nested function_identifier nodes)", fn.Name)
	}
	if fn.Params != 3 {
		t.Errorf("params = %d, want 3 (tf_port_item children)", fn.Params)
	}
	if fn.Cyclomatic != 11 {
		t.Errorf("cyclomatic = %d, want 11", fn.Cyclomatic)
	}
	if fn.Cognitive != 8 {
		t.Errorf("cognitive = %d, want 8", fn.Cognitive)
	}
	if fn.MaxNesting != 2 {
		t.Errorf("maxNesting = %d, want 2", fn.MaxNesting)
	}
	if fn.NPath == 0 {
		t.Error("npath = 0; the control flow was not lowered at all")
	}
}

// A module is a file-level container, not a unit of behaviour, so it must not
// be measured as a function — the same call a SQL table gets.
func TestVerilogModuleIsNotAFunction(t *testing.T) {
	src := `module m;
  function automatic int f(int a);
    return a;
  endfunction
endmodule`
	fs := analyze(t, parser.LangVerilog, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1 (the function, not the module); got %+v", len(fs.Functions), fs.Functions)
	}
	if fs.Functions[0].Name != "f" {
		t.Errorf("name = %q, want f", fs.Functions[0].Name)
	}
}

// A class constructor keeps its statements directly on
// class_constructor_declaration — there is no function_body_declaration to
// descend into — so the body resolution has to fall back to the declaration
// itself or every constructor would measure zero.
func TestVerilogConstructorBody(t *testing.T) {
	src := `class C;
  function new(int a);
    if (a > 0) begin
      x = 1;
    end
  endfunction
endclass`
	fs := analyze(t, parser.LangVerilog, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	fn := fs.Functions[0]
	if fn.Name != "new" {
		t.Errorf("name = %q, want new", fn.Name)
	}
	if fn.Cyclomatic != 2 {
		t.Errorf("cyclomatic = %d, want 2 (the constructor's if was not lowered)", fn.Cyclomatic)
	}
	if fn.Volume == 0 {
		t.Error("volume = 0; the constructor's body was not found")
	}
}

// Fan-out reads a call's callee off the first named child, because tf_call
// carries no fields at all.
func TestVerilogFanOut(t *testing.T) {
	src := `module m;
  function automatic int f(int a);
    return add_one(a) + add_one(a) + double_it(a);
  endfunction
endmodule`
	fs := analyze(t, parser.LangVerilog, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	// Two distinct callees; add_one appears twice and must count once.
	if fs.Functions[0].FanOut != 2 {
		t.Errorf("fanOut = %d, want 2 (distinct callees)", fs.Functions[0].FanOut)
	}
}
