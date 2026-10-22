package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ─── frames → GIF ───────────────────────────────────────────────────────

// frame is one frame of an animation: the scene file and how long it shows.
type frame struct {
	path string
	ms   int
}

// readFrames returns an animation's frames in order: <NNNN>_<ms>.ans, the
// number putting them in order and the milliseconds saying how long each one
// shows.
func readFrames(dir string) ([]frame, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var frames []frame
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".ans") {
			continue
		}
		base := strings.TrimSuffix(name, ".ans")
		i := strings.LastIndex(base, "_")
		if i < 0 {
			return nil, fmt.Errorf("%s: no milliseconds in the name", name)
		}
		ms, err := strconv.Atoi(base[i+1:])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		frames = append(frames, frame{path: filepath.Join(dir, name), ms: ms})
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames in %q", dir)
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].path < frames[j].path })
	return frames, nil
}

// writeGIF renders the frames and writes them as an animated GIF.
//
// Every frame is captured with Chrome, then drawn over the one before it:
// only the part that changed is stored, so the GIF stays small.
func writeGIF(chrome string, l layout, title string, frames []frame, out string, scale int) error {
	dir, err := os.MkdirTemp("", "ouija-gif-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// The frames, one PNG each.
	pngs := make([]string, len(frames))
	for i, f := range frames {
		src, err := os.ReadFile(f.path)
		if err != nil {
			return err
		}
		page := l.page(title, parseANSI(string(src), l.cols, l.rows))
		pngs[i] = filepath.Join(dir, fmt.Sprintf("%04d.png", i))
		if err := capture(chrome, page, pngs[i], l.width, l.height, scale); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(f.path), err)
		}
	}

	pal, index, err := buildPalette(pngs)
	if err != nil {
		return err
	}

	var images []*image.Paletted
	var delays []int
	var disposal []byte
	var prev *image.Paletted
	for i, path := range pngs {
		cur, err := quantize(path, pal, index)
		if err != nil {
			return err
		}
		if prev == nil {
			images = append(images, cur)
			delays = append(delays, delay(frames[i].ms))
			disposal = append(disposal, gif.DisposalNone)
			prev = cur
			continue
		}
		box := changedBox(prev, cur)
		if box.Empty() {
			// The picture did not change: the frame only shows longer.
			delays[len(delays)-1] += delay(frames[i].ms)
			continue
		}
		images = append(images, crop(cur, box))
		delays = append(delays, delay(frames[i].ms))
		disposal = append(disposal, gif.DisposalNone)
		prev = cur
	}

	bounds := images[0].Bounds()
	g := &gif.GIF{
		Image:    images,
		Delay:    delays,
		Disposal: disposal,
		Config: image.Config{
			ColorModel: pal,
			Width:      bounds.Dx(),
			Height:     bounds.Dy(),
		},
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := gif.EncodeAll(f, g); err != nil {
		return err
	}
	return f.Close()
}

// delay turns milliseconds into a GIF's hundredths of a second.
func delay(ms int) int {
	if ms <= 0 {
		return 1
	}
	d := (ms + 5) / 10
	if d < 1 {
		d = 1
	}
	return d
}

// ─── colors ─────────────────────────────────────────────────────────────

// buildPalette returns the GIF's palette and the index of each color in it:
// the colors the frames use, the most used first.
func buildPalette(paths []string) (color.Palette, map[uint32]uint8, error) {
	counts := map[uint32]int{}
	for _, path := range paths {
		img, err := decode(path)
		if err != nil {
			return nil, nil, err
		}
		bounds := img.Bounds()
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				counts[key(img.At(x, y))]++
			}
		}
	}
	type colorCount struct {
		key   uint32
		count int
	}
	list := make([]colorCount, 0, len(counts))
	for k, c := range counts {
		list = append(list, colorCount{key: k, count: c})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].key < list[j].key
	})

	// The palette holds the colors the frames use the most.
	pal := make(color.Palette, 0, 256)
	index := map[uint32]uint8{}
	for _, c := range list {
		if len(pal) == 256 {
			break
		}
		r, g, b, _ := split(c.key)
		pal = append(pal, color.NRGBA{R: r, G: g, B: b, A: 255})
		index[c.key] = uint8(len(pal) - 1)
	}
	return pal, index, nil
}

// quantize draws the PNG with the palette's colors: the ones the image has
// exactly, and for the rest the closest one.
func quantize(path string, pal color.Palette, index map[uint32]uint8) (*image.Paletted, error) {
	img, err := decode(path)
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	out := image.NewPaletted(bounds, pal)
	// The colors the image does not have exactly keep their closest one.
	closest := map[uint32]uint8{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			k := key(img.At(x, y))
			i, ok := index[k]
			if !ok {
				i, ok = closest[k]
				if !ok {
					i = nearest(k, pal)
					closest[k] = i
				}
			}
			out.SetColorIndex(x, y, i)
		}
	}
	return out, nil
}

// nearest returns the palette's index of the color closest to k.
func nearest(k uint32, pal color.Palette) uint8 {
	r, g, b, _ := split(k)
	best, dist := 0, int64(1<<62)
	for i, c := range pal {
		if _, _, _, a := c.RGBA(); a == 0 {
			continue
		}
		cr, cg, cb, _ := c.RGBA()
		dr, dg, db := int64(r)-int64(cr>>8), int64(g)-int64(cg>>8), int64(b)-int64(cb>>8)
		d := dr*dr + dg*dg + db*db
		if d < dist {
			best, dist = i, d
		}
	}
	return uint8(best)
}

// changedBox returns the rectangle where two frames differ.
func changedBox(a, b *image.Paletted) image.Rectangle {
	ab, bb := a.Bounds(), b.Bounds()
	if ab != bb {
		return bb
	}
	minX, minY := bb.Max.X, bb.Max.Y
	maxX, maxY := bb.Min.X, bb.Min.Y
	for y := bb.Min.Y; y < bb.Max.Y; y++ {
		for x := bb.Min.X; x < bb.Max.X; x++ {
			if a.Pix[a.PixOffset(x, y)] != b.Pix[b.PixOffset(x, y)] {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// crop returns the part of the frame the rectangle covers.
func crop(img *image.Paletted, r image.Rectangle) *image.Paletted {
	out := image.NewPaletted(r, img.Palette)
	draw.Draw(out, r, img, r.Min, draw.Src)
	return out
}

// ─── pixels ─────────────────────────────────────────────────────────────

// decode reads a PNG.
func decode(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return img, nil
}

// key packs a color into one number.
func key(c color.Color) uint32 {
	r, g, b, a := c.RGBA()
	return uint32(r>>8)<<24 | uint32(g>>8)<<16 | uint32(b>>8)<<8 | uint32(a>>8)
}

// split unpacks a color packed by key.
func split(k uint32) (r, g, b, a uint8) {
	return uint8(k >> 24), uint8(k >> 16), uint8(k >> 8), uint8(k)
}
