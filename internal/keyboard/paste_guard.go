package keyboard

import "fmt"

type clipboardSnapshot struct {
	text     string
	sequence uint32
}

type pasteClipboard interface {
	write(string) (clipboardSnapshot, error)
	read() (clipboardSnapshot, error)
	sequence() uint32
	restore(clipboardSnapshot, string) (bool, error)
}

func matchesClipboard(got, want clipboardSnapshot) bool {
	return want.sequence != 0 && got == want
}

func guardedPaste(clipboard pasteClipboard, text string, settle func(), paste func(), fallback func(error)) (result clipboardSnapshot, failure error) {
	defer func() {
		if failure != nil {
			fallback(failure)
		}
	}()
	written, err := clipboard.write(text)
	if err != nil {
		return clipboardSnapshot{}, fmt.Errorf("clipboard write: %w", err)
	}
	if written.text != text || written.sequence == 0 {
		return clipboardSnapshot{}, fmt.Errorf("clipboard write not confirmed")
	}
	for attempt := 0; attempt < 3; attempt++ {
		settle()
		current, err := clipboard.read()
		if err != nil {
			continue
		}
		if !matchesClipboard(current, written) {
			return written, fmt.Errorf("clipboard replaced before paste")
		}
		current, err = clipboard.read()
		if err != nil {
			continue
		}
		if !matchesClipboard(current, written) || clipboard.sequence() != written.sequence {
			return written, fmt.Errorf("clipboard changed at paste dispatch")
		}
		paste()
		return written, nil
	}
	return written, fmt.Errorf("clipboard verification unavailable after bounded retry")
}
