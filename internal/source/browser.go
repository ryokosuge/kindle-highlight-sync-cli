package source

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

// closeTimeout は Close が Chrome に閉じる要求を出してから、応じるのを待つ時間。
// 過ぎたら Chrome を kill し、終わるまで待つ。
const closeTimeout = 10 * time.Second

// Options は Chrome の起動の設定。login と sync は同じ ExecPath と ProfileDir を渡す（仕様書 §38）。
type Options struct {
	// Headless が false なら window を出す（login と sync --headed）。
	Headless bool
	// ProfileDir は Chrome の user data directory（仕様書 §38 の browser-profile）。
	ProfileDir string
	// ExecPath は Chrome の実行ファイル。空なら chromedp が既定の場所から探す。
	ExecPath string
}

// Browser は Open で起動した Chrome。使い終わったら Close する。
type Browser struct {
	// parent は Open に渡された ctx。Close が、Chrome が終わった理由が取り消しかどうかを見分けるのに使う。
	parent      context.Context
	ctx         context.Context
	cancel      context.CancelFunc
	cancelAlloc context.CancelFunc
}

// flags は Chrome の起動フラグを組み立てる。
// 仕様書 §38 で開けた条件（組 A）どおり、chromedp の DefaultExecAllocatorOptions を
// そのまま使い（--use-mock-keychain と --password-store=basic を含む）、headed では headless の値だけを false にする。
func flags(opts Options) []chromedp.ExecAllocatorOption {
	f := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", opts.Headless),
		chromedp.UserDataDir(opts.ProfileDir),
	)
	if opts.ExecPath != "" {
		f = append(f, chromedp.ExecPath(opts.ExecPath))
	}
	return f
}

// discard は chromedp のログを捨てる。chromedp は既定で log.Printf に書き、エラーのログも同じ関数に流す。
func discard(string, ...any) {}

// Open は opts で Chrome を起動する。起動できなければ exitcode.Browser（仕様書 §71 の 5）の種類のエラーを返す。
// Chrome は ctx が切れると終わる。
func Open(ctx context.Context, opts Options) (*Browser, error) {
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, flags(opts)...)
	bctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(discard))
	if err := chromedp.Do(bctx); err != nil {
		cancel()
		cancelAlloc()
		return nil, exitcode.New(exitcode.Browser, fmt.Errorf("source: start browser: %w", err))
	}
	return &Browser{parent: ctx, ctx: bctx, cancel: cancel, cancelAlloc: cancelAlloc}, nil
}

// Close は Chrome に閉じる要求を出し、Chrome のプロセスが終わるまで待ってから戻る。
// Chrome が closeTimeout までに応じなければ kill して、終わるまで待つ。
// Chrome が既に終わっていれば（window が閉じられた、ctx が切れた）待つだけで nil を返す。
// 閉じる要求の最中に Open の ctx が取り消されたときも、終わるのを待ってから nil を返す。
// それ以外で閉じる要求に失敗したときは、終わるのを待ってから exitcode.Browser の種類のエラーを返す。
func (b *Browser) Close() error {
	var err error
	if b.ctx.Err() == nil {
		tctx, cancel := context.WithTimeout(b.ctx, closeTimeout)
		err = chromedp.Cancel(tctx)
		cancel()
	}
	b.cancel()
	b.cancelAlloc()
	if err != nil && b.parent.Err() == nil {
		return exitcode.New(exitcode.Browser, fmt.Errorf("source: close browser: %w", err))
	}
	return nil
}
