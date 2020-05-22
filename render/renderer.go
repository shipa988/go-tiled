/*
Copyright (c) 2017 Lauris Bukšis-Haberkorns <lauris@nix.lv>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package render

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"

	"image/gif"

	"github.com/disintegration/imaging"
)

var (
	// ErrUnsupportedOrientation represents an error in the unsupported orientation for rendering.
	ErrUnsupportedOrientation = errors.New("tiled/render: unsupported orientation")
	// ErrUnsupportedRenderOrder represents an error in the unsupported order for rendering.
	ErrUnsupportedRenderOrder = errors.New("tiled/render: unsupported render order")
)

// RendererEngine is the interface implemented by objects that provide rendering engine for Tiled maps.
type RendererEngine interface {
	Init(m *tiled.Map)
	GetFinalImageSize() image.Rectangle
	RotateTileImage(tile *tiled.LayerTile, img image.Image) image.Image
	GetTilePosition(x, y int) image.Rectangle
	GetTrueTilePosition(tileRect image.Rectangle, x, y int) image.Rectangle
}

// Renderer represents an rendering engine.
type Renderer struct {
	m                  *tiled.Map
	Result             *image.NRGBA // The image result after rendering using the Render functions.
	tileCache          map[uint32]image.Image
	tileCollisionCache map[uint32]image.Rectangle
	engine             RendererEngine
}

type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

// NewRenderer creates new rendering engine instance.
func NewRenderer(m *tiled.Map) (*Renderer, error) {
	r := &Renderer{m: m, tileCache: make(map[uint32]image.Image)}
	if r.m.Orientation == "orthogonal" {
		r.engine = &OrthogonalRendererEngine{}
	} else {
		return nil, ErrUnsupportedOrientation
	}

	r.engine.Init(r.m)
	r.Clear()

	return r, nil
}

func (r *Renderer) getTileImage(tile *tiled.LayerTile) (image.Image, error) {
	timg, ok := r.tileCache[tile.Tileset.FirstGID+tile.ID]
	if ok {
		return r.engine.RotateTileImage(tile, timg), nil
	}
	// Precache all tiles in tileset
	if tile.Tileset.Image == nil {
		for i := 0; i < len(tile.Tileset.Tiles); i++ {
			if tile.Tileset.Tiles[i].ID == tile.ID {
				sf, err := os.Open(tile.Tileset.GetFileFullPath(tile.Tileset.Tiles[i].Image.Source))
				if err != nil {
					return nil, err
				}
				defer sf.Close()
				timg, _, err = image.Decode(sf)
				if err != nil {
					return nil, err
				}
				r.tileCache[tile.Tileset.FirstGID+tile.ID] = timg
			}
		}
	} else {
		l := r.m.Loader
		var img image.Image
		if l == nil || l.FileSystem == nil {
			sf, err := os.Open(tile.Tileset.GetFileFullPath(tile.Tileset.Image.Source))
			if err != nil {
				return nil, err
			}
			img, _, err = image.Decode(sf)
			if err != nil {
				return nil, err
			}
			sf.Close()
		} else {
			sf, err := l.FileSystem.Open(tile.Tileset.GetFileFullPath(tile.Tileset.Image.Source))

			if err != nil {
				return nil, err
			}
			img, _, err = image.Decode(sf)
			if err != nil {
				return nil, err
			}
			sf.Close()
		}

		tilesetTileCount := tile.Tileset.TileCount

		tilesetColumns := tile.Tileset.Columns

		margin := tile.Tileset.Margin

		spacing := tile.Tileset.Spacing

		if tilesetColumns == 0 {
			tilesetColumns = tile.Tileset.Image.Width / (tile.Tileset.TileWidth + spacing)
		}

		if tilesetTileCount == 0 {
			tilesetTileCount = (tile.Tileset.Image.Height / (tile.Tileset.TileHeight + spacing)) * tilesetColumns
		}

		for i := tile.Tileset.FirstGID; i < tile.Tileset.FirstGID+uint32(tilesetTileCount); i++ {
			x := int(i-tile.Tileset.FirstGID) % tilesetColumns
			y := int(i-tile.Tileset.FirstGID) / tilesetColumns

			xOffset := int(x)*spacing + margin
			yOffset := int(y)*spacing + margin

			rect := image.Rect(x*tile.Tileset.TileWidth+xOffset,
				y*tile.Tileset.TileHeight+yOffset,
				(x+1)*tile.Tileset.TileWidth+xOffset,
				(y+1)*tile.Tileset.TileHeight+yOffset)

			r.tileCache[i] = imaging.Crop(img, rect)
			if tile.ID == i-tile.Tileset.FirstGID {
				timg = r.tileCache[i]
			}
		}
	}

	return r.engine.RotateTileImage(tile, timg), nil
}

type TileObject struct {
	TileImage image.Image
	TilePos   image.Rectangle
}
type Coll struct {
	TileObjects []TileObject
	ColmapX     map[float64][]float64
	ColmapY     map[float64][]float64
}

// RenderLayer renders single map layer.
func (r *Renderer) RenderLayer(index int) (Coll, error) {
	colmapX := map[float64][]float64{}
	colmapY := map[float64][]float64{}
	coll := Coll{}
	layer := r.m.Layers[index]

	var xs, xe, xi, ys, ye, yi int
	if r.m.RenderOrder == "" || r.m.RenderOrder == "right-down" {
		xs = 0
		xe = r.m.Width
		xi = 1
		ys = 0
		ye = r.m.Height
		yi = 1
	} else {
		return coll, ErrUnsupportedRenderOrder
	}

	i := 0
	for y := ys; y*yi < ye; y = y + yi {
		for x := xs; x*xi < xe; x = x + xi {
			if layer.Tiles[i].IsNil() {
				i++
				continue
			}

			img, err := r.getTileImage(layer.Tiles[i])
			if err != nil {
				return coll, err
			}

			pos := r.engine.GetTrueTilePosition(img.Bounds(), x, y)
			//pos = r.engine.GetTilePosition(x, y)
			for _, collision := range layer.Tiles[i].Coll {
				if collision.Max.Y != 0 {
					pymin := float64(pos.Min.Y + collision.Min.Y)
					pymax := float64(pos.Min.Y + collision.Max.Y)
					pxmin := float64(pos.Min.X + collision.Min.X)
					pxmax := float64(pos.Min.X + collision.Max.X)
					for y := pymin; y <= pymax; y++ {
						for x := pxmin; x <= pxmax; x++ {
							colmapY[y] = append(colmapY[y], x)
							colmapX[x] = append(colmapX[x], y)
						}
					}
				}
			}
			coll.TileObjects = append(coll.TileObjects, TileObject{
				TileImage: img,
				TilePos:   pos,
			})

			if layer.Opacity < 1 {
				mask := image.NewUniform(color.Alpha{uint8(layer.Opacity * 255)})

				draw.DrawMask(r.Result, pos, img, img.Bounds().Min, mask, mask.Bounds().Min, draw.Over)
			} else {
				draw.Draw(r.Result, pos, img, img.Bounds().Min, draw.Over)
			}

			i++
		}
	}
	//func (Rectangle) Overlaps
	coll.ColmapY = colmapY
	coll.ColmapX = colmapX
	return coll, nil
}

// RenderVisibleLayers renders all visible map layers.
func (r *Renderer) RenderVisibleLayers() (coll Coll, e error) {
	coll = Coll{
		ColmapX: map[float64][]float64{},
		ColmapY: map[float64][]float64{},
	}

	for i := range r.m.Layers {
		if !r.m.Layers[i].Visible {
			continue
		}

		layerCollisions, err := r.RenderLayer(i)
		if err != nil {
			return coll, err
		}

		for k, v := range layerCollisions.ColmapX {
			coll.ColmapX[k] = append(coll.ColmapX[k], v...)
		}
		for k, v := range layerCollisions.ColmapY {
			coll.ColmapY[k] = append(coll.ColmapY[k], v...)
		}
		coll.TileObjects = append(coll.TileObjects, layerCollisions.TileObjects...)
	}
	return coll, nil
}

// Clear clears the render result to allow for separation of layers. For example, you can
// render a layer, make a copy of the render, clear the renderer, and repeat for each
// layer in the Map.
func (r *Renderer) Clear() {
	r.Result = image.NewNRGBA(r.engine.GetFinalImageSize())
}

// SaveAsPng writes rendered layers as PNG image to provided writer.
func (r *Renderer) SaveAsPng(w io.Writer) error {
	return png.Encode(w, r.Result)
}

// SaveAsJpeg writes rendered layers as JPEG image to provided writer.
func (r *Renderer) SaveAsJpeg(w io.Writer, options *jpeg.Options) error {
	return jpeg.Encode(w, r.Result, options)
}

// SaveAsGif writes rendered layers as GIF image to provided writer.
func (r *Renderer) SaveAsGif(w io.Writer, options *gif.Options) error {
	return gif.Encode(w, r.Result, options)
}
