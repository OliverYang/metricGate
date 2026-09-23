package sample;

// SplitBrain carries two disjoint method groups on disjoint fields: the CK
// engine must flag its cohesion debt on the class declaration line.
public class SplitBrain {
    private int left;
    private int right;
    private int extra;

    public int readLeft() { return left; }
    public int addLeft(int d) { left += d; return left; }
    public int moreLeft(int d) { left += d; return left + d; }
    public int mostLeft(int d) { left += d; return left + d + d; }

    public int readRight() { return right; }
    public int addRight(int d) { right += d; return right; }
    public int moreRight(int d) { right += d; return right + d; }
    public int mostRight(int d) { right += d; return right + d + d; }

    public int readExtra() { return extra; }
    public int addExtra(int d) { extra += d; return extra; }
    public int moreExtra(int d) { extra += d; return extra + d; }
    public int mostExtra(int d) { extra += d; return extra + d + d; }
}
