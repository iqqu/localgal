//go:build placeholders

package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"golocalgal/types"
	"hash/fnv"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var PlaceholderVideoCache = make(map[int][]byte)

func handleMedia(w http.ResponseWriter, r *http.Request) {
	rCtx := r.Context()
	p, err := perfTracker(rCtx, func(ctx context.Context, perf *types.Perf) error {
		return sendPlaceholderMedia(r.URL.Path, w, r)
	})
	if err != nil {
		_ = p // TODO add performance response headers...
		ctxErr := rCtx.Err()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(ctxErr, context.Canceled) || errors.Is(ctxErr, context.DeadlineExceeded) {
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

func LoadPlaceholderVideos(videosTarGz []byte) error {
	gzReader, err := gzip.NewReader(bytes.NewReader(videosTarGz))
	if err != nil {
		return fmt.Errorf("decompressing gzip: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		filename := filepath.Base(header.Name)
		index, err := strconv.Atoi(strings.TrimSuffix(filename, filepath.Ext(filename)))
		if err != nil {
			log.Printf("Skipping invalid filename: %s", header.Name)
			continue
		}

		videoData := make([]byte, header.Size)
		if _, err := io.ReadFull(tarReader, videoData); err != nil {
			return fmt.Errorf("reading video %d: %w", index, err)
		}

		PlaceholderVideoCache[index] = videoData
	}
	return nil
}

func sendPlaceholderMedia(seed string, w http.ResponseWriter, r *http.Request) error {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	pcg := rand.NewPCG(h.Sum64(), 0)
	randSeeded := rand.New(pcg)

	if strings.Contains(r.Header.Get("Accept"), "video/") || strings.HasSuffix(r.URL.Path, ".mp4") {
		return sendPlaceholderVideo(randSeeded, w)
	}
	if strings.HasSuffix(r.URL.Path, ".jpg") || strings.HasSuffix(r.URL.Path, ".jpeg") {
		return sendPlaceholderJpeg(randSeeded, w)
	}
	return sendPlaceholderPng(randSeeded, w)
}

func sendPlaceholderPng(randSeeded *rand.Rand, w http.ResponseWriter) error {
	img := generatePlaceholderImage(randSeeded)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Type", "image/png")
	return png.Encode(w, img)
}

func sendPlaceholderJpeg(randSeeded *rand.Rand, w http.ResponseWriter) error {
	img := generatePlaceholderImage(randSeeded)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Type", "image/jpeg")
	return jpeg.Encode(w, img, nil)
}

func sendPlaceholderVideo(randSeeded *rand.Rand, w http.ResponseWriter) error {
	img := generatePlaceholderVideo(randSeeded)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Type", "video/mp4")
	_, err := w.Write(img)
	return err
}

func generatePlaceholderImage(randSeeded *rand.Rand) *image.RGBA {
	const minDim, maxDim = 600, 1300
	width := randSeeded.IntN(maxDim-minDim+1) + minDim
	height := randSeeded.IntN(maxDim-minDim+1) + minDim

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	colBase := &color.RGBA{
		R: uint8(randSeeded.IntN(256)),
		G: uint8(randSeeded.IntN(256)),
		B: uint8(randSeeded.IntN(256)),
		A: 0xff,
	}
	colShift := &color.RGBA{
		R: colBase.R + 40, // uint8 wraps around
		G: colBase.G + 40,
		B: colBase.B + 40,
		A: 0xff,
	}
	lerp := func(base *color.RGBA, shift *color.RGBA, t float32) *color.RGBA {
		// a + (b-a)*t
		colNew := &color.RGBA{
			R: uint8(float32(base.R) + (float32(shift.R)-float32(base.R))*t),
			G: uint8(float32(base.G) + (float32(shift.G)-float32(base.G))*t),
			B: uint8(float32(base.B) + (float32(shift.B)-float32(base.B))*t),
			A: 0xff,
		}
		return colNew
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			colPx := lerp(colBase, colShift, (float32(x)/float32(width))*float32(y)/float32(height))
			img.Set(x, y, colPx)
		}
	}
	return img
}

func generatePlaceholderVideo(randSeeded *rand.Rand) []byte {
	const minDim, maxDim = 600, 1400
	width := randSeeded.IntN(maxDim-minDim+1) + minDim
	height := randSeeded.IntN(maxDim-minDim+1) + minDim

	videoIndex := randSeeded.IntN(len(PlaceholderVideoCache))
	videoData := PlaceholderVideoCache[videoIndex]

	modifiedVideo := modifyVideoDimensions(videoData, width, height)
	return modifiedVideo
}

// modifyVideoDimensions changes the display dimension metadata only
func modifyVideoDimensions(videoData []byte, width, height int) []byte {
	modified := make([]byte, len(videoData))
	copy(modified, videoData)

	// Find and modify the tkhd box (track header) - contains display width/height
	tkhdOffset := findBox(modified, "tkhd")
	if tkhdOffset != -1 {
		// tkhd box:
		// uint8 version (1 byte)
		// [24]bit flags (3 bytes)
		headerOffset := 8 // boxtype: int32, version: uint8, flags: 24 bits
		ver := uint8(modified[tkhdOffset+headerOffset])
		versionOffset := 0
		if ver == 1 {
			versionOffset = versionOffset +
				8 + // creation time
				8 + // modification time
				4 + // track id
				4 + // reserved
				8 + // duration
				0
		} else { // assume 0
			versionOffset = versionOffset +
				4 + // creation time
				4 + // modification time
				4 + // track id
				4 + // reserved
				4 + // duration
				0
		}

		// 8 + 32 + 4*2 + 8 + 4*9

		dimensionOffset := tkhdOffset + headerOffset + versionOffset +
			4*2 + // reserved
			2 + // layer
			2 + // alternate group
			2 + // volume
			2 + // reserved
			4*9 + // unity matrix
			0

		widthOffset := dimensionOffset + 4
		heightOffset := dimensionOffset + 8

		// tkhd box structure (version 0):
		// [00-07] header, [08-11] version+flags,
		// [12-15] creation time, [16-19] modification time,
		// [20-23] track ID, [24-27] reserved,
		// [28-31] duration, [32-39] reserved,
		// [40-41] layer, [42-43] alternate group, [44-45] volume, [46-47] reserved,
		// [48-83] matrix (36 bytes),
		// [84-87] width (fixed-point 16.16), [88-91] height (fixed-point 16.16)

		newWidth := uint32(width) << 16
		binary.BigEndian.PutUint32(modified[widthOffset:], newWidth)

		newHeight := uint32(height) << 16
		binary.BigEndian.PutUint32(modified[heightOffset:], newHeight)
	}

	return modified
}

func findBox(data []byte, boxType string) int {
	return walkBoxes(data, boxType, 0)
}

// walkBoxes recursively searches for an mp4 box
func walkBoxes(data []byte, boxType string, offset int) int {
	i := offset
	for i < len(data)-8 {
		if i+8 > len(data) {
			break
		}
		boxSize := int(binary.BigEndian.Uint32(data[i:]))
		if boxSize < 8 || i+boxSize > len(data) {
			break
		}

		currentBoxType := string(data[i+4 : i+8])
		if currentBoxType == boxType {
			return i
		}

		if isContainerBox(currentBoxType) {
			if result := walkBoxes(data, boxType, i+8); result != 1 {
				return result
			}
		}
		i += boxSize
	}
	return -1
}

func isContainerBox(boxType string) bool {
	containers := []string{
		"moov",
		"moof",
		"mfra",
		"meta",
		"trak",
		"mvex",
		"edts",
		"mdia",
		"udta",
		"minf",
		"dinf",
		"stbl",
		"strk",
		"traf",
		"ipro",
		"fiin",
		"paen",
		"sinf",
	}
	return slices.Contains(containers, boxType)
}
