// Command uigif records the README's web-UI preview: it drives a running
// `tallybook --serve` in a headless Chrome, screenshots the page as it moves
// through the screens (mostly the overview), and writes the frames plus an ffmpeg concat script
// that turns them into docs/web-ui.gif.
//
// Usage: go run ./tools/uigif -url http://127.0.0.1:7477 -out /tmp/frames
// then: ffmpeg -f concat -safe 0 -i /tmp/frames/frames.txt -vf "fps=20,scale=1000:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=96:stats_mode=diff[p];[s1][p]paletteuse=dither=none:diff_mode=rectangle" -loop 0 docs/web-ui.gif
//
// 20 fps keeps the 50 ms scroll frames; no dither because the UI is flat colour and dithering only adds noise and bytes.
//
// It takes no personal data of its own: whatever the served page shows is
// what ends up in the frames, so run it against a scope you are happy to
// publish (a --project filter, or a copy of the ledger).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:7477", "the running tallybook --serve URL")
	out := flag.String("out", "frames", "directory for the PNG frames")
	project := flag.String("project", "", "project filter to select in the scope bar before recording")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.WindowSize(1280, 860))
	actx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel2 := chromedp.NewContext(actx)
	defer cancel2()
	ctx, cancel3 := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel3()

	list, err := os.Create(filepath.Join(*out, "frames.txt"))
	if err != nil {
		log.Fatal(err)
	}
	defer list.Close()
	n := 0
	// shot writes one frame and holds it for hold seconds in the concat list.
	shot := func(hold float64) chromedp.Action {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			var buf []byte
			if err := chromedp.CaptureScreenshot(&buf).Do(ctx); err != nil {
				return err
			}
			n++
			name := fmt.Sprintf("f%03d.png", n)
			if err := os.WriteFile(filepath.Join(*out, name), buf, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(list, "file '%s'\nduration %.2f\n", name, hold)
			return nil
		})
	}
	settle := func() chromedp.Action {
		return chromedp.Tasks{
			chromedp.Sleep(150 * time.Millisecond),
			chromedp.WaitNotPresent(`#view[aria-busy="true"]`, chromedp.ByQuery),
			chromedp.Sleep(250 * time.Millisecond),
		}
	}
	go_ := func(hash string) chromedp.Action {
		return chromedp.Tasks{chromedp.Navigate(*url + "/#/" + hash), settle()}
	}
	// scroll eases the page from where it is to y over a dozen short frames,
	// so the GIF shows a scroll rather than a cut. The frames are 50 ms each,
	// which needs the GIF rendered at 20 fps to keep them.
	scroll := func(y int, hold float64) chromedp.Action {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			var from float64
			if err := chromedp.Evaluate("window.scrollY", &from).Do(ctx); err != nil {
				return err
			}
			const steps = 12
			for i := 1; i <= steps; i++ {
				t := float64(i) / steps
				eased := 1 - (1-t)*(1-t)*(1-t) // ease-out cubic
				pos := from + (float64(y)-from)*eased
				if err := chromedp.Evaluate(fmt.Sprintf("window.scrollTo({top:%d,behavior:'instant'})", int(pos)), nil).Do(ctx); err != nil {
					return err
				}
				if err := chromedp.Sleep(30 * time.Millisecond).Do(ctx); err != nil {
					return err
				}
				if err := shot(0.05).Do(ctx); err != nil {
					return err
				}
			}
			return shot(hold).Do(ctx)
		})
	}
	// hover moves the pointer onto the nth element matching sel, so hover
	// states (a bar's tooltip, a row's highlight) are what the frame shows.
	hover := func(sel string, n int, hold float64) chromedp.Action {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			var pt []float64
			// n >= 0 is the nth match in document order; n < 0 is the nth
			// tallest (-1 the tallest), which is how a bar worth hovering is
			// found in a chart where most days are empty.
			js := fmt.Sprintf(`(() => { let els = [...document.querySelectorAll(%q)]; let n = %d; if (n < 0) { els.sort((a, b) => b.getBoundingClientRect().height - a.getBoundingClientRect().height); n = -n - 1; } const el = els[n]; if (!el) return [0, 0]; const r = el.getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; })()`, sel, n)
			if err := chromedp.Evaluate(js, &pt).Do(ctx); err != nil {
				return err
			}
			if pt[0] == 0 && pt[1] == 0 {
				return nil // not on this page; skip the frame rather than fail the recording
			}
			if err := chromedp.MouseEvent(input.MouseMoved, pt[0], pt[1]).Do(ctx); err != nil {
				return err
			}
			if err := chromedp.Sleep(160 * time.Millisecond).Do(ctx); err != nil {
				return err
			}
			return shot(hold).Do(ctx)
		})
	}
	click := func(sel string, hold float64) chromedp.Action {
		return chromedp.Tasks{chromedp.Click(sel, chromedp.ByQuery), settle(), shot(hold)}
	}

	// The overview is the page people live on, so it takes well over half of
	// the recording: read it, hover the busiest days and the findings, compare
	// with the window before, then narrow to one project with the picker,
	// open a finding and come back. The other screens get a glance each. The
	// overview never shows a path, so it is recorded over every project; the
	// project filter is applied before the screens that list sessions.
	tasks := chromedp.Tasks{
		chromedp.EmulateViewport(1280, 860),
		go_("report"),
		shot(2.6),
		hover("#daychart rect", -1, 1.5),
		hover("#daychart rect", -2, 1.3),
		hover("#daychart rect", -3, 1.1),
		hover(".tile", 3, 1.0),
		scroll(430, 1.2),
		hover(".bar", 0, 1.1),
		hover(".bar", 1, 0.8),
		scroll(900, 1.3),
		hover(".finding-row", 0, 1.2),
		hover(".finding-row", 1, 1.2),
		scroll(0, 0.6),
		// A week against the week before is the comparison people actually
		// make, and it is the one every ledger old enough to run this has.
		chromedp.SetValue("#f-since", "7d", chromedp.ByQuery), settle(), shot(1.4),
		click("#cmp", 3.2),
		scroll(430, 1.6),
	}
	if *project != "" {
		tasks = append(tasks,
			scroll(0, 0.6),
			chromedp.SetValue("#f-project", *project, chromedp.ByQuery), settle(), shot(2.2),
		)
	}
	tasks = append(tasks,
		scroll(900, 1.2),
		hover(".finding-row", 0, 0.9),
		click(".finding-row", 2.4),
		scroll(700, 1.8),
		go_("report"), shot(1.8),
		go_("findings"), shot(2.0),
		go_("agents"), shot(1.8),
		go_("sessions"), shot(1.4),
		hover("tr.row", 0, 0.8),
		click("tr.row a.mono", 2.0),
		go_("report"), shot(2.6),
	)
	if err := chromedp.Run(ctx, tasks); err != nil {
		log.Fatal(err)
	}
	// ffmpeg's concat demuxer wants the last file repeated without a duration.
	fmt.Fprintf(list, "file 'f%03d.png'\n", n)
	fmt.Printf("%d frames in %s\n", n, *out)
}
