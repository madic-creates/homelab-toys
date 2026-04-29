package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestRenderSprite_ContainsMoodClass(t *testing.T) {
	tests := []string{"ecstatic", "happy", "meh", "sick", "dying"}
	for _, mood := range tests {
		t.Run(mood, func(t *testing.T) {
			svg := RenderSprite(mood, false)
			if !strings.Contains(svg, `class="sprite mood-`+mood+`"`) {
				t.Errorf("mood %q: missing class, got: %s", mood, svg[:min(200, len(svg))])
			}
			if !strings.Contains(svg, `viewBox="0 0 64 64"`) {
				t.Errorf("mood %q: missing viewBox", mood)
			}
		})
	}
}

func TestRenderSprite_Confused_AppendsQuestionMark(t *testing.T) {
	plain := RenderSprite("happy", false)
	confused := RenderSprite("happy", true)
	if confused == plain {
		t.Fatal("confused output identical to plain")
	}
	// The "?" overlay is rendered as a <text> element near top-right.
	if !strings.Contains(confused, "<text") || !strings.Contains(confused, "?") {
		t.Error("confused variant missing <text>?</text> overlay")
	}
}

func TestRenderSprite_DyingPixelsAreInBottomQuarter(t *testing.T) {
	// Dying mood is "lying flat" per spec — the matrix only fills rows
	// 50+. Verify no rects are emitted with a y coordinate above row 30,
	// which distinguishes it from the other (head-up) moods.
	svg := RenderSprite("dying", false)
	for y := 0; y < 30; y++ {
		needle := `<rect x="`
		marker := `" y="` + strconv.Itoa(y) + `"`
		// Walk every <rect ...> tag; the y attribute follows x in the
		// emitter's format string.
		idx := 0
		for {
			i := strings.Index(svg[idx:], needle)
			if i < 0 {
				break
			}
			tag := svg[idx+i:]
			end := strings.Index(tag, "/>")
			if end < 0 {
				break
			}
			if strings.Contains(tag[:end], marker) {
				t.Errorf("dying sprite has a rect at y=%d (above row 30); should be lying flat", y)
				return
			}
			idx += i + end + 2
		}
	}
}

func TestRenderSprite_RectCountReasonable(t *testing.T) {
	// Each non-space cell becomes one <rect>. Sanity-check that a 64×64
	// matrix doesn't accidentally emit > 1500 rects (would mean the
	// matrix is mostly filled, which is wrong for a pet sprite).
	svg := RenderSprite("happy", false)
	rectCount := strings.Count(svg, "<rect")
	if rectCount < 30 || rectCount > 1500 {
		t.Errorf("rect count = %d, expected 30..1500", rectCount)
	}
}

func TestRenderSprite_UnknownMoodFallsBackToHappy(t *testing.T) {
	svg := RenderSprite("definitely-not-a-mood", false)
	// Plan documents the fallback: unknown mood is rewritten to "happy"
	// and the matrix used is matrices["happy"].
	if !strings.Contains(svg, `class="sprite mood-happy"`) {
		t.Errorf("unknown mood should fall back to happy; got: %s", svg[:min(200, len(svg))])
	}
}
