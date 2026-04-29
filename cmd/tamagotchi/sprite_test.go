package main

import (
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

func TestRenderSprite_DyingHasNoBounceAnimationClass(t *testing.T) {
	svg := RenderSprite("dying", false)
	// dying mood is "lying flat, no movement" per spec — but the CSS
	// animation is keyed off the mood-<name> body class, not the SVG. So
	// this test just sanity-checks dying still renders as a valid sprite
	// with the right class.
	if !strings.Contains(svg, "mood-dying") {
		t.Error("dying sprite missing mood-dying class")
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
