package tui

import "github.com/atotto/clipboard"

var clipboardWriteAll = clipboard.WriteAll

func copyTextToClipboard(text string) error {
	return clipboardWriteAll(text)
}
