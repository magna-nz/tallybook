// Command uigif records the README's web-UI preview: it drives a running
// `tallybook --serve` in a headless Chrome, screenshots the page as it moves
// through the screens, and writes the frames plus an ffmpeg concat script
// that turns them into docs/web-ui.gif.
//
// Usage: go run ./tools/uigif -url http://127.0.0.1:7477 -out /tmp/frames
// then: ffmpeg -f concat -safe 0 -i /tmp/frames/frames.txt -vf "fps=10,scale=1100:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer:bayer_scale=5" docs/web-ui.gif
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
	scrollTo := func(y int) chromedp.Action {
		return chromedp.Tasks{chromedp.Evaluate(fmt.Sprintf("window.scrollTo({top:%d,behavior:'instant'})", y), nil), chromedp.Sleep(120 * time.Millisecond)}
	}

	tasks := chromedp.Tasks{
		chromedp.EmulateViewport(1280, 860),
		go_("report"),
	}
	if *project != "" {
		tasks = append(tasks, chromedp.SetValue("#f-project", *project, chromedp.ByQuery), settle())
	}
	tasks = append(tasks,
		shot(2.4),
		scrollTo(420), shot(1.8),
		scrollTo(900), shot(2.2),
		go_("findings"), shot(2.2),
		chromedp.Click(".finding-row", chromedp.ByQuery), settle(), shot(2.6),
		scrollTo(700), shot(2.0),
		go_("agents"), shot(2.0),
		go_("sessions"), shot(2.0),
		chromedp.Click("tr.row a.mono", chromedp.ByQuery), settle(), shot(2.6),
		scrollTo(520), shot(2.2),
		go_("changes"), shot(2.0),
		go_("report"), chromedp.Click(`#f-since`, chromedp.ByQuery), chromedp.SetValue("#f-since", "7d", chromedp.ByQuery), settle(), shot(1.6),
		chromedp.Click("#cmp", chromedp.ByQuery), settle(), shot(2.6),
	)
	if err := chromedp.Run(ctx, tasks); err != nil {
		log.Fatal(err)
	}
	// ffmpeg's concat demuxer wants the last file repeated without a duration.
	fmt.Fprintf(list, "file 'f%03d.png'\n", n)
	fmt.Printf("%d frames in %s\n", n, *out)
}
