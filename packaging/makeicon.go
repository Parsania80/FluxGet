//go:build ignore

// Generate the Windows icon from the established app artwork using only Go's standard library.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
)

func main() {
	f, err := os.Open("desktop/assets/fluxget.png")
	if err != nil {
		panic(err)
	}
	source, err := png.Decode(f)
	f.Close()
	if err != nil {
		panic(err)
	}
	icon := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			icon.Set(x, y, source.At(x*source.Bounds().Dx()/256, y*source.Bounds().Dy()/256))
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, icon); err != nil {
		panic(err)
	}
	var ico bytes.Buffer
	for _, v := range []uint16{0, 1, 1} {
		binary.Write(&ico, binary.LittleEndian, v)
	}
	ico.Write([]byte{0, 0, 0, 0})
	for _, v := range []uint16{1, 32} {
		binary.Write(&ico, binary.LittleEndian, v)
	}
	binary.Write(&ico, binary.LittleEndian, uint32(encoded.Len()))
	binary.Write(&ico, binary.LittleEndian, uint32(22))
	ico.Write(encoded.Bytes())
	if err := os.WriteFile("desktop/assets/fluxget.ico", ico.Bytes(), 0644); err != nil {
		panic(err)
	}
}
