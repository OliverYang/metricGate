package com.example.calc;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * Dense fixture for the complexity differential (internal/metrics/
 * legacy_diff_test.go). Every construct the Java lowerer has to recognise
 * appears here at least once, and several appear nested inside each other,
 * because that is where a lowering bug hides.
 */
public class Calculator {

    private int memo;
    private final List<String> trace = new ArrayList<>();

    // if / else-if chain / else, and the nesting each link pays.
    public String classify(int a, int b) {
        if (a > 0 && b > 0) {
            return "both";
        } else if (a > 0) {
            return "left";
        } else if (b > 0) {
            return "right";
        } else {
            return "neither";
        }
    }

    // every loop form, nested.
    public int loops(int n) {
        int total = 0;
        for (int i = 0; i < n; i++) {
            total++;
        }
        for (String s : trace) {
            while (total < 0) {
                total = 1;
            }
            do {
                total--;
            } while (total > n);
        }
        return total;
    }

    // colon-style switch with fall-through labels and a default arm.
    public int switchColon(int mode) {
        int out = 0;
        switch (mode) {
            case 1:
            case 2:
                out = mode;
            case 3:
                out += 1;
                break;
            default:
                out = -1;
        }
        return out;
    }

    // arrow-style switch: block arm, expression arm, default.
    public String switchArrow(String key) {
        return switch (key) {
            case "a", "b" -> "ab";
            case "c" -> {
                memo = memo + 1;
                yield "c";
            }
            default -> "?";
        };
    }

    // ternary chains and logical runs of mixed operators.
    public int expressions(Integer boxed, boolean flag) {
        int v = boxed == null ? 0 : (boxed > 10 ? 2 : 3);
        if (flag && v > 0 || !flag && v < 0) {
            v++;
        }
        if (flag && !flag && flag || v == 1 || v == 2) {
            v += 2;
        }
        return v;
    }

    // try / catch / finally, with control flow inside the handler.
    public void handling(List<Integer> xs) {
        try {
            for (int x : xs) {
                if (x < 0) {
                    throw new IllegalStateException("neg");
                }
            }
        } catch (IllegalStateException e) {
            for (int i = 0; i < 3; i++) {
                trace.add(e.getMessage());
            }
        } catch (RuntimeException e) {
            memo = 0;
        } finally {
            if (memo > 0) {
                memo = memo / 2;
            }
        }
    }

    // lambdas, including ones nested in calls, ternaries and other lambdas.
    public List<Integer> functional(List<Integer> xs, Map<String, Integer> idx) {
        List<Integer> out = new ArrayList<>();
        xs.stream()
                .filter(x -> x > 0 && x < 100)
                .map(x -> x % 2 == 0 ? x / 2 : x * 3 + 1)
                .forEach(v -> {
                    if (v > 10) {
                        out.add(v);
                    } else {
                        Runnable r = () -> {
                            for (int i = 0; i < 2; i++) {
                                memo++;
                            }
                        };
                        r.run();
                    }
                });
        Runnable tail = idx.isEmpty() ? () -> memo = 0 : () -> {
            while (memo > 0) {
                memo--;
            }
        };
        tail.run();
        return out;
    }

    // labeled break / continue and a nested anonymous class with its own method.
    public int labeled(int[][] grid) {
        outer:
        for (int[] row : grid) {
            for (int cell : row) {
                if (cell == 0) {
                    continue outer;
                }
                if (cell < 0) {
                    break outer;
                }
                memo += cell;
            }
        }
        Runnable r = new Runnable() {
            @Override
            public void run() {
                if (memo > 0 && grid.length > 0) {
                    for (int i = 0; i < memo; i++) {
                        trace.add("x");
                    }
                }
            }
        };
        r.run();
        return memo;
    }

    // recursion: a call whose name is the enclosing method's own.
    public long fib(int n) {
        if (n < 2) {
            return n;
        }
        return fib(n - 1) + fib(n - 2);
    }

    // constructor with a delegating call and an early guard.
    public Calculator(int seed) {
        this();
        if (seed > 0) {
            while (seed-- > 1) {
                memo += seed % 3;
            }
        }
    }

    public Calculator() {
        Optional.of(1).ifPresent(v -> memo = v);
    }

    static {
        int x = 0;
        for (int i = 0; i < 10; i++) {
            x += i;
        }
    }
}
