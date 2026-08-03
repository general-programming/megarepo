// Package progress renders live byte-transfer progress for the long, silent
// steps of `barf device update`: the ~700MB firmware download and the mirror
// upload of the same bytes. Both used to produce no output at all, so a slow
// transfer was indistinguishable from a hang.
//
// A Sink travels on the context, so firmware -- which has no idea whether it
// is attached to a terminal -- reports bytes without importing cli. The
// rendering half (Reporter) is the only part that knows about terminals, and
// it emits ANSI/carriage-return redraws ONLY in terminal mode; everything
// else gets throttled, newline-delimited lines that survive being piped.
package progress

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Op is what a transfer does. The word must be a regular verb: the log lines
// are built as Op+"ing" and Op+"ed".
type Op string

const (
	OpDownload Op = "download"
	OpUpload   Op = "upload"
)

func (o Op) present() string { return string(o) + "ing" }
func (o Op) past() string    { return string(o) + "ed" }

// Transfer describes one byte transfer about to start.
type Transfer struct {
	Op   Op
	Name string
	// Total is the expected byte count; 0 means unknown (no percentage, no ETA).
	Total int64
	// URL is where the bytes come from or go to; used in failure lines only.
	URL string
}

// Sink observes one transfer at a time. Implementations must tolerate calls
// from a single transfer's goroutine and be safe to share between transfers.
type Sink interface {
	// Start announces a transfer. Any transfer still open is superseded.
	Start(t Transfer)
	// Update reports the CUMULATIVE bytes moved so far.
	Update(transferred int64)
	// Finish closes the current transfer; err != nil means it failed partway.
	Finish(transferred int64, err error)
	// Skip reports a transfer that never happened, e.g. a cache hit. Without
	// it, reusing a cached image looks exactly like an instant download.
	Skip(t Transfer, reason string)
}

// Discard is the Sink used when nobody is watching.
var Discard Sink = nopSink{}

type nopSink struct{}

func (nopSink) Start(Transfer)        {}
func (nopSink) Update(int64)          {}
func (nopSink) Finish(int64, error)   {}
func (nopSink) Skip(Transfer, string) {}

type sinkKey struct{}

// WithSink attaches a Sink to ctx so downstream transfers report to it.
func WithSink(ctx context.Context, s Sink) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, sinkKey{}, s)
}

// FromContext returns the attached Sink, or Discard. Never nil, so callers
// need no reporting-enabled branch.
func FromContext(ctx context.Context) Sink {
	if ctx == nil {
		return Discard
	}
	if s, ok := ctx.Value(sinkKey{}).(Sink); ok && s != nil {
		return s
	}
	return Discard
}

// Options configures a Reporter.
type Options struct {
	// Prefix goes in front of every line, e.g. "[sea1-leaf-0] ".
	Prefix string
	// Terminal enables in-place redraws. FALSE for --plain and for anything
	// that is not a terminal: those must never see ANSI or carriage returns.
	Terminal bool
	// Interval is the minimum gap between progress lines. Zero means 200ms on
	// a terminal (smooth) and 5s otherwise (a readable piped log).
	Interval time.Duration
	// StepPercent additionally emits a line every N% of a known total when not
	// on a terminal. Zero means 10; negative disables it.
	StepPercent int
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Reporter is a Sink that writes human-readable progress to an io.Writer.
type Reporter struct {
	out  io.Writer
	opts Options

	mu       sync.Mutex
	active   bool
	transfer Transfer
	started  time.Time
	lastAt   time.Time
	lastStep int
	// dirty means a redrawn line is on screen and must be cleared first.
	dirty bool
}

// NewReporter builds a Reporter writing to out.
func NewReporter(out io.Writer, opts Options) *Reporter {
	if out == nil {
		out = io.Discard
	}
	return &Reporter{out: out, opts: opts}
}

func (r *Reporter) now() time.Time {
	if r.opts.Now != nil {
		return r.opts.Now()
	}
	return time.Now()
}

func (r *Reporter) interval() time.Duration {
	if r.opts.Interval > 0 {
		return r.opts.Interval
	}
	if r.opts.Terminal {
		return 200 * time.Millisecond
	}
	return 5 * time.Second
}

func (r *Reporter) stepPercent() int {
	if r.opts.StepPercent < 0 {
		return 0
	}
	if r.opts.StepPercent == 0 {
		return 10
	}
	return r.opts.StepPercent
}

// Start implements Sink.
func (r *Reporter) Start(t Transfer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.clearLine()
	r.active = true
	r.transfer = t
	r.started = r.now()
	r.lastAt = r.started
	r.lastStep = 0

	size := ""
	if t.Total > 0 {
		size = " (" + Bytes(t.Total) + ")"
	}
	fmt.Fprintf(r.out, "%s%s %s%s\n", r.opts.Prefix, t.Op.present(), t.Name, size)
}

// Update implements Sink.
func (r *Reporter) Update(transferred int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	now := r.now()
	if !r.due(now, transferred) {
		return
	}
	r.lastAt = now
	r.lastStep = r.step(transferred)

	line := r.opts.Prefix + "  " + r.body(transferred, now)
	if r.opts.Terminal {
		// \r + erase-to-end-of-line: one line, rewritten in place.
		fmt.Fprintf(r.out, "\r%s\x1b[K", line)
		r.dirty = true
		return
	}
	fmt.Fprintln(r.out, line)
}

// due throttles rendering: a time gap always qualifies, and off a terminal a
// percentage step does too, so a stalled-looking log still advances by 10%.
func (r *Reporter) due(now time.Time, transferred int64) bool {
	if now.Sub(r.lastAt) >= r.interval() {
		return true
	}
	if !r.opts.Terminal && r.step(transferred) > r.lastStep {
		return true
	}
	return false
}

func (r *Reporter) step(transferred int64) int {
	size := r.stepPercent()
	if size <= 0 || r.transfer.Total <= 0 {
		return 0
	}
	return percent(transferred, r.transfer.Total) / size
}

// Finish implements Sink.
func (r *Reporter) Finish(transferred int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	r.clearLine()
	t := r.transfer
	elapsed := r.now().Sub(r.started)
	r.active = false

	if err != nil {
		of := ""
		if t.Total > 0 {
			of = fmt.Sprintf(" of %s (%d%%)", Bytes(t.Total), percent(transferred, t.Total))
		}
		from := ""
		if t.URL != "" {
			from = " from " + t.URL
		}
		fmt.Fprintf(r.out, "%s%s of %s FAILED after %s%s%s\n",
			r.opts.Prefix, string(t.Op), t.Name, Bytes(transferred), of, from)
		return
	}
	fmt.Fprintf(r.out, "%s%s %s: %s in %s (%s)\n",
		r.opts.Prefix, t.Op.past(), t.Name, Bytes(transferred), Duration(elapsed), rate(transferred, elapsed))
}

// Skip implements Sink.
func (r *Reporter) Skip(t Transfer, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearLine()
	size := ""
	if t.Total > 0 {
		size = " (" + Bytes(t.Total) + ")"
	}
	fmt.Fprintf(r.out, "%sno %s needed for %s%s: %s\n",
		r.opts.Prefix, string(t.Op), t.Name, size, reason)
}

// clearLine wipes a pending in-place line so the next full line starts clean.
// A no-op off a terminal, where nothing was ever redrawn.
func (r *Reporter) clearLine() {
	if r.dirty {
		fmt.Fprint(r.out, "\r\x1b[K")
		r.dirty = false
	}
}

// body is the middle of a progress line: percent, bar, counts, rate, ETA.
func (r *Reporter) body(transferred int64, now time.Time) string {
	total := r.transfer.Total
	elapsed := now.Sub(r.started)
	perSecond := bytesPerSecond(transferred, elapsed)

	var segs []string
	if total > 0 {
		pct := percent(transferred, total)
		segs = append(segs, fmt.Sprintf("%3d%%", pct))
		if r.opts.Terminal {
			segs = append(segs, bar(pct, 24))
		}
		segs = append(segs, fmt.Sprintf("%s / %s", Bytes(transferred), Bytes(total)))
	} else {
		segs = append(segs, Bytes(transferred))
	}
	segs = append(segs, rate(transferred, elapsed))
	if total > 0 && perSecond > 0 && transferred < total {
		remaining := time.Duration(float64(total-transferred) / perSecond * float64(time.Second))
		segs = append(segs, "ETA "+Duration(remaining))
	}
	return strings.Join(segs, "  ")
}

// bar is a plain-ASCII meter; no ANSI, so it is safe wherever it is drawn.
func bar(pct, width int) string {
	if width <= 0 {
		return ""
	}
	filled := pct * width / 100
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(".", width-filled) + "]"
}

func percent(n, total int64) int {
	if total <= 0 {
		return 0
	}
	p := int(n * 100 / total)
	if p > 100 {
		return 100
	}
	if p < 0 {
		return 0
	}
	return p
}

func bytesPerSecond(n int64, elapsed time.Duration) float64 {
	if elapsed <= 0 || n <= 0 {
		return 0
	}
	return float64(n) / elapsed.Seconds()
}

func rate(n int64, elapsed time.Duration) string {
	perSecond := bytesPerSecond(n, elapsed)
	if perSecond <= 0 {
		return "-- B/s"
	}
	return Bytes(int64(perSecond)) + "/s"
}

// Bytes renders a byte count the way an operator reads it: "712.4 MiB".
func Bytes(n int64) string {
	const unit = 1024
	if n > -unit && n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit || m <= -unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Duration renders an elapsed time or ETA as m:ss, or h:mm:ss past an hour.
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int64(d.Round(time.Second).Seconds())
	hours, minutes, secs := seconds/3600, (seconds%3600)/60, seconds%60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

// Writer counts bytes written through it and reports them to a Sink. Wrapping
// the DESTINATION (not the source) means progress only ever counts bytes that
// actually landed. It passes every byte through untouched, so the file on
// disk is identical with or without progress.
type Writer struct {
	w    io.Writer
	sink Sink
	n    int64
}

// NewWriter wraps w, reporting cumulative writes to sink.
func NewWriter(w io.Writer, sink Sink) *Writer {
	if sink == nil {
		sink = Discard
	}
	return &Writer{w: w, sink: sink}
}

func (c *Writer) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.n += int64(n)
		c.sink.Update(c.n)
	}
	return n, err
}

// N is the number of bytes written so far.
func (c *Writer) N() int64 { return c.n }

// Reader counts bytes read through it, for uploads where the transfer is
// driven by something else reading our file.
type Reader struct {
	r    io.Reader
	sink Sink
	n    int64
}

// NewReader wraps r, reporting cumulative reads to sink.
func NewReader(r io.Reader, sink Sink) *Reader {
	if sink == nil {
		sink = Discard
	}
	return &Reader{r: r, sink: sink}
}

func (c *Reader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		c.sink.Update(c.n)
	}
	return n, err
}

// N is the number of bytes read so far.
func (c *Reader) N() int64 { return c.n }
