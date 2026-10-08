package com.example.shapes;

import java.io.Closeable;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Supplier;

/**
 * Second dense fixture for the complexity differential (internal/metrics/
 * legacy_diff_test.go). Calculator.java covers the constructs that appear in
 * ordinary methods; this one concentrates on the shapes around them — nested
 * types, interfaces and enums (whose methods the per-file pass charges to both
 * themselves and their enclosing unit), resources and multi-catch handlers,
 * brace-less branches, and decisions buried inside an arrow switch arm.
 */
public class Shapes {

    private int count;
    private final List<String> log = new ArrayList<>();

    // brace-less if/else: the branches hold one statement each, and the
    // nesting level still opens.
    public void bare(int a) {
        if (a > 0) log.add("pos");
        else log.add("non-pos");
    }

    // try-with-resources plus a multi-catch handler.
    public String resources(Closeable c, boolean boom) throws IOException {
        try (c) {
            if (boom) {
                throw new IOException("boom");
            }
            return "ok";
        } catch (IOException | IllegalStateException e) {
            count++;
            return "io";
        } catch (RuntimeException e) {
            for (int i = 0; i < 2; i++) {
                log.add(e.getMessage());
            }
            return "runtime";
        }
    }

    // synchronized block with a guard loop inside a switch arm.
    public int guarded(int mode, Object payload) {
        synchronized (this) {
            switch (mode) {
                case 1:
                    while (count > 0 && payload != null) {
                        count--;
                    }
                    break;
                case 2:
                    if (payload instanceof String s) {
                        count += s.length();
                    }
                    break;
                default:
                    count = 0;
            }
        }
        return count;
    }

    // a lambda inside a loop inside a lambda, with a ternary for a body.
    public List<Supplier<String>> nested(boolean flag) {
        List<Supplier<String>> out = new ArrayList<>();
        for (int i = 0; i < 3; i++) {
            out.add(() -> {
                for (String s : log) {
                    if (s.isEmpty()) {
                        continue;
                    }
                    out.add(() -> flag ? s : s + "!");
                }
                return flag ? "a" : "b";
            });
        }
        return out;
    }

    // labeled block with a break out of a switch inside a do-while.
    public int scan(int[] xs) {
        int hits = 0;
        walk:
        do {
            for (int x : xs) {
                switch (x) {
                    case 0:
                        continue walk;
                    case 7:
                        break walk;
                    default:
                        hits += x > 0 ? 1 : 0;
                }
            }
        } while (hits < 0);
        return hits;
    }

    // A nested class: its method is measured as its own unit *and* lowered
    // transparently into the enclosing one, exactly as the old walk did.
    public class Inner {
        public int twice(int v) {
            if (v > 0) {
                return v + v;
            }
            return -twice(-v);
        }
    }

    // Interface and enum members go through the same lowerer.
    public interface Contract {
        int apply(int v);

        default int orZero(int v) {
            return v > 0 ? apply(v) : 0;
        }

        static Contract always(int v) {
            return x -> {
                if (x > v) {
                    return x - v;
                }
                return 0;
            };
        }
    }

    public enum Kind {
        HOT, COLD;

        private final int code = 1;

        public String label(int flip) {
            for (int i = 0; i < code; i++) {
                if (flip > 0 && i == flip) {
                    return name() + "-" + i;
                }
            }
            return name();
        }
    }
}
