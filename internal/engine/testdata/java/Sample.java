package sample;

// TODO: this comment is detected when todo-comment is enabled.

public class Sample {

    public int chain(int a, int b) {
        if (a > 0 && b > 0) {
            for (int i = 0; i < a; i++) {
                if (i > b || a > 100) {
                    System.out.println(i);
                }
            }
        }
        switch (a) {
            case 1:
                return 1;
            case 2:
                return 2;
            default:
                return 0;
        }
    }

    public void empty(int x) {
        if (x > 0) {
        }
        while (x > 0) {
            x--;
        }
    }

    public void swallow() {
        try {
            int x = 1;
        } catch (RuntimeException e) {
        }
    }

    public void boom() {
        try {
            throw new RuntimeException("boom");
        } catch (RuntimeException e) {
            e.printStackTrace();
        }
    }

    public int many(int a, int b, int c, int d, int e, int f, int g) {
        return a + b + c + d + e + f + g;
    }
}
