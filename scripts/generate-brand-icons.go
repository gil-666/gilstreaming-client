//go:build ignore

// Generates the platform icon containers from the canonical GilStreaming PNG.
// Run from the repository root with: go run scripts/generate-brand-icons.go
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

type renderedIcon struct {
	size int
	png  []byte
}

func main() {
	sourceFile, err := os.Open("gilstreaming-logo.png")
	must(err)
	defer sourceFile.Close()

	source, err := png.Decode(sourceFile)
	must(err)

	sizes := []int{16, 24, 32, 48, 64, 128, 256, 512}
	icons := make([]renderedIcon, 0, len(sizes))
	for _, size := range sizes {
		var encoded bytes.Buffer
		must(png.Encode(&encoded, resize(source, size)))
		icons = append(icons, renderedIcon{size: size, png: encoded.Bytes()})
	}

	must(os.MkdirAll("app/res", 0755))
	must(os.MkdirAll("app/deploy/steamlink", 0755))
	must(copyFile("gilstreaming-logo.png", "app/res/gilstreaming-logo.png"))
	must(copyFile("gilstreaming-logo.png", "app/deploy/steamlink/gilstreaming.png"))
	must(writeICO("app/gilstreaming.ico", icons[:len(icons)-1]))
	must(writeICNS("app/gilstreaming.icns", icons))
}

func resize(source image.Image, size int) *image.NRGBA {
	destination := image.NewNRGBA(image.Rect(0, 0, size, size))
	bounds := source.Bounds()
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := (float64(x)+0.5)*float64(bounds.Dx())/float64(size) - 0.5
			sy := (float64(y)+0.5)*float64(bounds.Dy())/float64(size) - 0.5
			x0 := clamp(int(math.Floor(sx)), 0, bounds.Dx()-1)
			y0 := clamp(int(math.Floor(sy)), 0, bounds.Dy()-1)
			x1 := clamp(x0+1, 0, bounds.Dx()-1)
			y1 := clamp(y0+1, 0, bounds.Dy()-1)
			wx := sx - math.Floor(sx)
			wy := sy - math.Floor(sy)

			r00, g00, b00, a00 := source.At(bounds.Min.X+x0, bounds.Min.Y+y0).RGBA()
			r10, g10, b10, a10 := source.At(bounds.Min.X+x1, bounds.Min.Y+y0).RGBA()
			r01, g01, b01, a01 := source.At(bounds.Min.X+x0, bounds.Min.Y+y1).RGBA()
			r11, g11, b11, a11 := source.At(bounds.Min.X+x1, bounds.Min.Y+y1).RGBA()
			r := bilerp(r00, r10, r01, r11, wx, wy)
			g := bilerp(g00, g10, g01, g11, wx, wy)
			b := bilerp(b00, b10, b01, b11, wx, wy)
			a := bilerp(a00, a10, a01, a11, wx, wy)
			if a > 0 {
				destination.SetNRGBA(x, y, imageToNRGBA(r, g, b, a))
			}
		}
	}
	return destination
}

func imageToNRGBA(r, g, b, a uint32) color.NRGBA {
	return color.NRGBA{
		R: uint8(min(r*0xffff/a, 0xffff) >> 8),
		G: uint8(min(g*0xffff/a, 0xffff) >> 8),
		B: uint8(min(b*0xffff/a, 0xffff) >> 8),
		A: uint8(a >> 8),
	}
}

func bilerp(v00, v10, v01, v11 uint32, x, y float64) uint32 {
	top := float64(v00)*(1-x) + float64(v10)*x
	bottom := float64(v01)*(1-x) + float64(v11)*x
	return uint32(top*(1-y) + bottom*y)
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func writeICO(path string, icons []renderedIcon) error {
	var output bytes.Buffer
	must(binary.Write(&output, binary.LittleEndian, uint16(0)))
	must(binary.Write(&output, binary.LittleEndian, uint16(1)))
	must(binary.Write(&output, binary.LittleEndian, uint16(len(icons))))

	offset := 6 + len(icons)*16
	for _, icon := range icons {
		dimension := byte(icon.size)
		if icon.size == 256 {
			dimension = 0
		}
		output.WriteByte(dimension)
		output.WriteByte(dimension)
		output.WriteByte(0)
		output.WriteByte(0)
		must(binary.Write(&output, binary.LittleEndian, uint16(1)))
		must(binary.Write(&output, binary.LittleEndian, uint16(32)))
		must(binary.Write(&output, binary.LittleEndian, uint32(len(icon.png))))
		must(binary.Write(&output, binary.LittleEndian, uint32(offset)))
		offset += len(icon.png)
	}
	for _, icon := range icons {
		output.Write(icon.png)
	}
	return os.WriteFile(path, output.Bytes(), 0644)
}

func writeICNS(path string, icons []renderedIcon) error {
	types := map[int][]string{
		16:  {"icp4"},
		32:  {"icp5", "ic11"},
		64:  {"icp6", "ic12"},
		128: {"ic07"},
		256: {"ic08", "ic13"},
		512: {"ic09", "ic14"},
	}
	var body bytes.Buffer
	for _, icon := range icons {
		iconTypes, ok := types[icon.size]
		if !ok {
			continue
		}
		for _, iconType := range iconTypes {
			body.WriteString(iconType)
			must(binary.Write(&body, binary.BigEndian, uint32(len(icon.png)+8)))
			body.Write(icon.png)
		}
	}

	var output bytes.Buffer
	output.WriteString("icns")
	must(binary.Write(&output, binary.BigEndian, uint32(body.Len()+8)))
	output.Write(body.Bytes())
	return os.WriteFile(path, output.Bytes(), 0644)
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Clean(destination), data, 0644)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
