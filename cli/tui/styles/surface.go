package styles

import (
	"image/color"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

// surface is a background lifted off the terminal's own: a fixed shade can't
// be "raised" on every theme (it read as a hole on lighter dark themes), so
// once the terminal reports its background, surfaces derive from it.
type surface struct {
	derived  *atomic.Pointer[color.RGBA]
	fallback color.Color
}

func (s surface) RGBA() (r, g, b, a uint32) {
	if derived := s.derived.Load(); derived != nil {
		return derived.RGBA()
	}
	return s.fallback.RGBA()
}

var (
	surfaceDerived       atomic.Pointer[color.RGBA]
	surfaceStrongDerived atomic.Pointer[color.RGBA]
	surfaceTurnDerived   atomic.Pointer[color.RGBA]

	// ColorSurface is a faint raised background: user turns, inline code,
	// the active tab.
	ColorSurface color.Color = surface{&surfaceDerived, adaptive("#F0F1F3", "#30343E")}
	// ColorSurfaceStrong is a clearly raised background: the selection.
	ColorSurfaceStrong color.Color = surface{&surfaceStrongDerived, adaptive("#E3E5E9", "#3B404C")}
	// ColorSurfaceTurn is a raised background tinted with Brand: the user's
	// turns, so they stand apart from the neutral selection.
	ColorSurfaceTurn color.Color = surface{&surfaceTurnDerived, adaptive("#ECE6FB", "#3D3654")}
)

// SetBackground adopts the terminal's reported background: adaptive colors
// follow its darkness and surfaces are lifted from it.
func SetBackground(background color.Color, dark bool) {
	compat.HasDarkBackground = dark
	lift := lipgloss.Lighten
	if !dark {
		lift = lipgloss.Darken
	}
	surfaceDerived.Store(rgba(lift(background, 0.05)))
	surfaceStrongDerived.Store(rgba(lift(background, 0.14)))
	surfaceTurnDerived.Store(mix(lift(background, 0.06), ColorBrand, 0.16))
}

// mix blends weight of tint into base.
func mix(base, tint color.Color, weight float64) *color.RGBA {
	b, t := rgba(base), rgba(tint)
	channel := func(from, to uint8) uint8 { return uint8(float64(from) + (float64(to)-float64(from))*weight) }
	return &color.RGBA{R: channel(b.R, t.R), G: channel(b.G, t.G), B: channel(b.B, t.B), A: 255}
}

func rgba(c color.Color) *color.RGBA {
	r, g, b, a := c.RGBA()
	return &color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}
