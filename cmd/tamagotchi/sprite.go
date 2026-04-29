package main

import (
	"fmt"
	"strings"
)

// spriteSize is the matrix dimension. Standalone view scales by 8 in CSS,
// widget by 2; image-rendering: pixelated keeps each cell crisp.
const spriteSize = 64

// moodMatrix is one mood's 64×64 grid. Space = transparent; any other
// rune is a palette index (single character, 0..9 or a..z).
type moodMatrix [spriteSize]string

// moodPalette maps single-character cell IDs to hex colour strings.
type moodPalette map[byte]string

// matrices and palettes are seeded with deliberately simple shapes so the
// total inline-SVG payload stays small. A real artist can refine these
// later — the structure is the contract.
//
// Layout convention (rough):
//
//	rows  0–6:  empty (room for the confused "?" overlay)
//	rows  7–14: reserved for future ear/antenna (currently empty)
//	rows 15–46: head (round-ish, centred on the canvas)
//	rows 47–58: body
//	rows 59–63: feet
//
// Per-mood differences:
//
//	ecstatic: wide smile, sparkles
//	happy:    standard smile
//	meh:      flat mouth
//	sick:     droopy eyes, slight green tint
//	dying:    lying flat — body shape compressed to bottom rows
var matrices = map[string]moodMatrix{
	"ecstatic": ecstaticMatrix(),
	"happy":    happyMatrix(),
	"meh":      mehMatrix(),
	"sick":     sickMatrix(),
	"dying":    dyingMatrix(),
}

var palettes = map[string]moodPalette{
	"ecstatic": {'1': "#ffd84a", '2': "#1d1d1d", '3': "#ff6f9c"},
	"happy":    {'1': "#ffd84a", '2': "#1d1d1d"},
	"meh":      {'1': "#cdc78a", '2': "#1d1d1d"},
	"sick":     {'1': "#9bc18a", '2': "#1d1d1d", '3': "#5a7a4f"},
	"dying":    {'1': "#7a7a7a", '2': "#1d1d1d"},
}

// RenderSprite emits one sprite as inline SVG. The wrapping <svg>
// element carries class="sprite mood-<name>" so per-mood CSS animations
// can target it. Confused=true overlays a "?" near the top-right.
func RenderSprite(mood string, confused bool) string {
	m, ok := matrices[mood]
	if !ok {
		m = matrices["happy"]
		mood = "happy"
	}
	palette := palettes[mood]

	var b strings.Builder
	b.Grow(8 * 1024)
	fmt.Fprintf(&b, `<svg class="sprite mood-%s" viewBox="0 0 64 64" xmlns="http://www.w3.org/2000/svg" shape-rendering="crispEdges">`, mood)

	for y, row := range m {
		for x := 0; x < len(row) && x < spriteSize; x++ {
			c := row[x]
			if c == ' ' {
				continue
			}
			colour, ok := palette[c]
			if !ok {
				continue // unknown palette index → skip
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="1" height="1" fill="%s"/>`, x, y, colour)
		}
	}

	if confused {
		// "?" floats above the head. Big enough to read at scale=2 (widget).
		b.WriteString(`<text x="48" y="10" font-family="monospace" font-size="10" font-weight="bold" fill="#fff" stroke="#000" stroke-width="0.4">?</text>`)
	}

	b.WriteString(`</svg>`)
	return b.String()
}

// happyMatrix returns the v1 happy-pet matrix. The shape is a round head
// with two dot-eyes and an upward-curving mouth, body below with two
// stub legs. Easy to tweak: each row is a 64-char string.
func happyMatrix() moodMatrix {
	return baseRoundShape("smile")
}

func ecstaticMatrix() moodMatrix {
	m := baseRoundShape("bigsmile")
	// Sparkles around the head (rows 12–18, cols 8 and 56).
	for _, p := range [][2]int{{12, 8}, {14, 56}, {16, 9}, {18, 55}} {
		row := []byte(m[p[0]])
		row[p[1]] = '3'
		m[p[0]] = string(row)
	}
	return m
}

func mehMatrix() moodMatrix {
	return baseRoundShape("flat")
}

func sickMatrix() moodMatrix {
	m := baseRoundShape("frown")
	// Recolor all eye-pixel rows (23–25, matching baseRoundShape's eye loop)
	// to palette index 3 (the droopy-green tint) for the sick look.
	for _, y := range []int{23, 24, 25} {
		row := []byte(m[y])
		if row[22] == '2' {
			row[22] = '3'
		}
		if row[42] == '2' {
			row[42] = '3'
		}
		m[y] = string(row)
	}
	return m
}

func dyingMatrix() moodMatrix {
	// Lying flat — fill only rows 50–60, body sideways. Head on the
	// right, feet sticking up at the left. No animation needed; the CSS
	// .mood-dying body class skips the bounce.
	var m moodMatrix
	for y := 50; y <= 60; y++ {
		row := make([]byte, spriteSize)
		for x := 0; x < spriteSize; x++ {
			row[x] = ' '
		}
		// Head circle on the right
		if y >= 51 && y <= 58 {
			for x := 36; x <= 56; x++ {
				row[x] = '1'
			}
		}
		// Body extending left
		if y >= 53 && y <= 56 {
			for x := 14; x < 36; x++ {
				row[x] = '1'
			}
		}
		// X-eyes on the head
		if y == 54 {
			row[44] = '2'
			row[48] = '2'
		}
		if y == 55 {
			row[45] = '2'
			row[47] = '2'
		}
		m[y] = string(row)
	}
	// Empty top rows
	for y := 0; y < 50; y++ {
		m[y] = strings.Repeat(" ", spriteSize)
	}
	for y := 61; y < 64; y++ {
		m[y] = strings.Repeat(" ", spriteSize)
	}
	return m
}

// baseRoundShape returns a head/body/legs sprite with the given mouth
// variant ("smile", "bigsmile", "flat", "frown"). Centralising this
// removes ~150 lines of literal-row duplication across moods.
func baseRoundShape(mouth string) moodMatrix {
	var m moodMatrix
	for y := 0; y < spriteSize; y++ {
		row := make([]byte, spriteSize)
		for x := 0; x < spriteSize; x++ {
			row[x] = ' '
		}
		// Head: rows 12..40, roughly circular around (32, 26) radius 14.
		if y >= 12 && y <= 40 {
			cy := 26
			r2 := 14 * 14
			for x := 0; x < spriteSize; x++ {
				dx := x - 32
				dy := y - cy
				if dx*dx+dy*dy <= r2 {
					row[x] = '1'
				}
			}
		}
		// Body: rows 41..56, narrower oval.
		if y >= 41 && y <= 56 {
			cy := 48
			r2 := 12 * 12
			for x := 0; x < spriteSize; x++ {
				dx := x - 32
				dy := y - cy
				if dx*dx+dy*dy <= r2 {
					row[x] = '1'
				}
			}
		}
		// Legs: rows 57..62, two stubs.
		if y >= 57 && y <= 62 {
			for x := 24; x <= 28; x++ {
				row[x] = '1'
			}
			for x := 36; x <= 40; x++ {
				row[x] = '1'
			}
		}
		m[y] = string(row)
	}
	// Eyes: dots at (22, 24) and (42, 24).
	for y := 23; y <= 25; y++ {
		row := []byte(m[y])
		row[22] = '2'
		row[42] = '2'
		m[y] = string(row)
	}
	// Mouth: rows 32–34 depending on variant.
	switch mouth {
	case "bigsmile":
		// Wide upturn: row 31 ends, row 32 middle, row 33 dips.
		drawMouth(&m, []struct{ y, lx, rx int }{{31, 24, 40}, {32, 25, 39}, {33, 28, 36}})
	case "smile":
		drawMouth(&m, []struct{ y, lx, rx int }{{32, 26, 38}, {33, 28, 36}})
	case "flat":
		drawMouth(&m, []struct{ y, lx, rx int }{{33, 27, 37}})
	case "frown":
		// Inverted: row 33 ends, row 32 middle.
		drawMouth(&m, []struct{ y, lx, rx int }{{33, 24, 40}, {32, 28, 36}})
	}
	return m
}

func drawMouth(m *moodMatrix, segs []struct{ y, lx, rx int }) {
	for _, seg := range segs {
		row := []byte(m[seg.y])
		for x := seg.lx; x <= seg.rx; x++ {
			row[x] = '2'
		}
		m[seg.y] = string(row)
	}
}
