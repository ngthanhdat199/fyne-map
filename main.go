package main

import (
	"fmt"
	"image"
	"image/png"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	minZoom       = 0
	maxZoom       = 18
	tileSize      = 256
	tileResultBuf = 64
	yourUserAgent = "MyFyneMapApp/0.1 (contact@example.com)"
)

var cartoSubdomains = []string{"a", "b", "c", "d"}

type TileCoord struct {
	Z, X, Y int
}

type TileResult struct {
	Coord TileCoord
	Image image.Image
	Error error
}

type TileMapWidget struct {
	widget.BaseWidget
	mu sync.RWMutex

	zoom           int
	centerLat      float64
	centerLon      float64
	width          float32
	height         float32
	imageDataCache map[TileCoord]image.Image
	tileFetching   map[TileCoord]bool
	resultChan     chan TileResult
	stopChan       chan struct{}
}

func NewTileMapWidget(startZoom int, startLat, startLon float64) *TileMapWidget {
	m := &TileMapWidget{
		zoom:           startZoom,
		centerLat:      startLat,
		centerLon:      startLon,
		imageDataCache: make(map[TileCoord]image.Image),
		tileFetching:   make(map[TileCoord]bool),
		resultChan:     make(chan TileResult, tileResultBuf),
		stopChan:       make(chan struct{}),
	}
	m.ExtendBaseWidget(m)
	return m
}

func (m *TileMapWidget) CreateRenderer() fyne.WidgetRenderer {
	r := &tileMapRenderer{
		mapWidget:   m,
		canvasTiles: make(map[TileCoord]*canvas.Image),
	}
	return r
}

func (m *TileMapWidget) Destroy() {
	close(m.stopChan)
}

func (m *TileMapWidget) Dragged(e *fyne.DragEvent) {
	m.mu.Lock()
	currentZoom := m.zoom
	currentLat := m.centerLat
	currentLon := m.centerLon
	m.mu.Unlock()

	centerX, centerY := latLonToTileXY(currentLat, currentLon, currentZoom)

	tileDragX := float64(e.Dragged.DX) / tileSize
	tileDragY := float64(e.Dragged.DY) / tileSize

	newCenterX := centerX - tileDragX
	newCenterY := centerY - tileDragY

	newLat, newLon := tileXYToLatLon(newCenterX, newCenterY, currentZoom)

	m.mu.Lock()
	m.centerLat = newLat
	m.centerLon = newLon
	m.mu.Unlock()

	m.clampView()
	m.Refresh()
}

func (m *TileMapWidget) DragEnd() {}

func tileXYToLatLon(xtile, ytile float64, zoom int) (lat, lon float64) {
	n := math.Pow(2.0, float64(zoom))
	lon = xtile/n*360.0 - 180.0
	latRad := math.Atan(math.Sinh(math.Pi * (1 - 2*ytile/n)))
	lat = latRad * 180.0 / math.Pi
	return lat, lon
}

func (m *TileMapWidget) Scrolled(e *fyne.ScrollEvent) {
	dx := e.Scrolled.DX
	dy := e.Scrolled.DY
	log.Printf("--- Scrolled method entered! Scrolled.DX: %.2f, Scrolled.DY: %.2f ---", dx, dy)

	if dy == 0 {
		log.Println("Scroll event received, but Scrolled.DY is zero. No zoom change.")
		return
	}

	m.mu.Lock()
	oldZoom := m.zoom
	zoomChanged := false

	if dy < 0 {
		if m.zoom < maxZoom {
			m.zoom++
			zoomChanged = true
			log.Println("Scroll Zoom In Attempt -> Zoom", m.zoom)
		} else {
			log.Println("Scroll Zoom In Attempt -> Already at max zoom", maxZoom)
		}
	} else if dy > 0 {
		if m.zoom > minZoom {
			m.zoom--
			zoomChanged = true
			log.Println("Scroll Zoom Out Attempt -> Zoom", m.zoom)
		} else {
			log.Println("Scroll Zoom Out Attempt -> Already at min zoom", minZoom)
		}
	}

	m.mu.Unlock()

	if zoomChanged {
		log.Printf("Zoom changed from %d to %d. Refreshing.", oldZoom, m.zoom)
		m.Refresh()
	} else {
		log.Printf("Zoom unchanged (%d) or at limit. No refresh triggered by scroll.", m.zoom)
	}
}

func (m *TileMapWidget) clampView() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.centerLat = math.Max(-85.0511, math.Min(85.0511, m.centerLat))
	m.centerLon = math.Mod(m.centerLon+180.0, 360.0) - 180.0
	if m.centerLon == -180 {
		m.centerLon = 180
	}
}

type tileMapRenderer struct {
	mapWidget   *TileMapWidget
	objects     []fyne.CanvasObject
	canvasTiles map[TileCoord]*canvas.Image
}

func (r *tileMapRenderer) Layout(size fyne.Size) {
	r.mapWidget.mu.Lock()
	r.mapWidget.width = size.Width
	r.mapWidget.height = size.Height
	r.mapWidget.mu.Unlock()
	r.mapWidget.Refresh()
}
func (r *tileMapRenderer) MinSize() fyne.Size { return fyne.NewSize(tileSize, tileSize) }

func (r *tileMapRenderer) Refresh() {
	r.processTileResults()

	r.mapWidget.mu.RLock()
	zoom := r.mapWidget.zoom
	centerLat := r.mapWidget.centerLat
	centerLon := r.mapWidget.centerLon
	width := r.mapWidget.width
	height := r.mapWidget.height
	r.mapWidget.mu.RUnlock()
	if width <= 0 || height <= 0 {
		return
	}

	visibleTiles := r.calculateRequiredTiles(zoom, centerLat, centerLon, width, height)
	currentObjects := make([]fyne.CanvasObject, 0, len(visibleTiles))
	neededCoords := make([]TileCoord, 0, len(visibleTiles))
	activeCanvasTiles := make(map[TileCoord]bool)

	r.mapWidget.mu.Lock()
	for _, coord := range visibleTiles {
		imgData, dataFound := r.mapWidget.imageDataCache[coord]
		if dataFound {
			canvasImg, canvasFound := r.canvasTiles[coord]
			if !canvasFound {
				canvasImg = canvas.NewImageFromImage(imgData)
				if canvasImg == nil {
					log.Printf("Error creating canvas image from cached data for tile %v", coord)
					delete(r.mapWidget.imageDataCache, coord)
					continue
				}
				canvasImg.ScaleMode = canvas.ImageScaleFastest
				canvasImg.FillMode = canvas.ImageFillOriginal
				canvasImg.Resize(fyne.NewSize(tileSize, tileSize))
				r.canvasTiles[coord] = canvasImg
			}
			posX, posY := r.calculateTilePosition(coord, zoom, centerLat, centerLon, width, height)
			canvasImg.Move(fyne.NewPos(posX, posY))
			canvasImg.Show()
			currentObjects = append(currentObjects, canvasImg)
			activeCanvasTiles[coord] = true
		} else {
			if !r.mapWidget.tileFetching[coord] {
				neededCoords = append(neededCoords, coord)
				r.mapWidget.tileFetching[coord] = true
			}
		}
	}
	r.mapWidget.mu.Unlock()

	for coord, img := range r.canvasTiles {
		if !activeCanvasTiles[coord] {
			img.Hide()
			delete(r.canvasTiles, coord)
		}
	}
	r.objects = currentObjects

	for _, coord := range neededCoords {
		go r.fetchTileDataAsync(coord)
	}

	canvas.Refresh(r.mapWidget)
}

func (r *tileMapRenderer) processTileResults() {
	for {
		select {
		case result := <-r.mapWidget.resultChan:
			r.mapWidget.mu.Lock()
			delete(r.mapWidget.tileFetching, result.Coord)
			if result.Error == nil && result.Image != nil {
				r.mapWidget.imageDataCache[result.Coord] = result.Image
			} else {
				log.Printf("Received error for tile %v from channel: %v", result.Coord, result.Error)
			}
			r.mapWidget.mu.Unlock()
		default:
			return
		}
	}
}

func (r *tileMapRenderer) calculateRequiredTiles(zoom int, lat, lon float64, w, h float32) []TileCoord {
	centerX, centerY := latLonToTileXY(lat, lon, zoom)
	tilesX := int(math.Ceil(float64(w)/tileSize)) + 2
	tilesY := int(math.Ceil(float64(h)/tileSize)) + 2
	startX := int(math.Floor(centerX - float64(tilesX)/2.0))
	startY := int(math.Floor(centerY - float64(tilesY)/2.0))
	tiles := make([]TileCoord, 0, tilesX*tilesY)
	maxTile := int(math.Pow(2, float64(zoom))) - 1
	for x := startX; x < startX+tilesX; x++ {
		for y := startY; y < startY+tilesY; y++ {
			wrappedX := x
			if maxTile > 0 {
				wrappedX = (x%(maxTile+1) + (maxTile + 1)) % (maxTile + 1)
			} else {
				wrappedX = 0
			}
			clampedY := clamp(y, 0, maxTile)
			tiles = append(tiles, TileCoord{Z: zoom, X: wrappedX, Y: clampedY})
		}
	}
	return tiles
}

func (r *tileMapRenderer) calculateTilePosition(coord TileCoord, zoom int, centerLat, centerLon float64, w, h float32) (float32, float32) {
	n := math.Pow(2.0, float64(zoom))
	centerPxX := ((centerLon + 180.0) / 360.0) * n * tileSize
	centerPxY := (1.0 - math.Log(math.Tan(centerLat*math.Pi/180.0)+1.0/math.Cos(centerLat*math.Pi/180.0))/math.Pi) / 2.0 * n * tileSize
	tilePxX := float64(coord.X) * tileSize
	tilePxY := float64(coord.Y) * tileSize
	offsetX := tilePxX - centerPxX
	offsetY := tilePxY - centerPxY
	screenX := (w / 2.0) + float32(offsetX)
	screenY := (h / 2.0) + float32(offsetY)
	return screenX, screenY
}

func (r *tileMapRenderer) fetchTileDataAsync(coord TileCoord) {
	result := TileResult{Coord: coord}

	defer func() {
		select {
		case r.mapWidget.resultChan <- result:
			if result.Error != nil {
				log.Printf("Sent error for tile %v to channel: %v", coord, result.Error)
				r.clearFetchingStatus(coord)
			}
		case <-r.mapWidget.stopChan:
			log.Printf("Widget destroyed, fetch cancelled for tile %v", coord)
			r.clearFetchingStatus(coord)
		case <-time.After(5 * time.Second):
			log.Printf("Timeout sending result for tile %v to channel", coord)
			r.clearFetchingStatus(coord)
		}
	}()

	httpClient := &http.Client{Timeout: 20 * time.Second}
	subdomainIndex := (coord.X + coord.Y) % len(cartoSubdomains)
	subdomain := cartoSubdomains[subdomainIndex]
	url := fmt.Sprintf("https://%s.basemaps.cartocdn.com/rastertiles/voyager/%d/%d/%d.png", subdomain, coord.Z, coord.X, coord.Y)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		result.Error = fmt.Errorf("creating request: %w", err)
		return
	}
	req.Header.Set("User-Agent", yourUserAgent)

	resp, err := httpClient.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		result.Error = fmt.Errorf("http do: %w", err)
		return
	}

	if resp.StatusCode == http.StatusNotFound {
		result.Error = fmt.Errorf("tile not found (404)")
		return
	}
	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Errorf("http status %s", resp.Status)
		return
	}

	imgData, err := png.Decode(resp.Body)
	if err != nil {
		result.Error = fmt.Errorf("decoding png: %w", err)
		return
	}

	result.Image = imgData
}

func (r *tileMapRenderer) clearFetchingStatus(coord TileCoord) {
	r.mapWidget.mu.Lock()
	delete(r.mapWidget.tileFetching, coord)
	r.mapWidget.mu.Unlock()
}

func (r *tileMapRenderer) Objects() []fyne.CanvasObject {
	r.mapWidget.mu.RLock()
	defer r.mapWidget.mu.RUnlock()
	objs := make([]fyne.CanvasObject, len(r.objects))
	copy(objs, r.objects)
	return objs
}

func (r *tileMapRenderer) Destroy() {}

func latLonToTileXY(lat, lon float64, zoom int) (float64, float64) {
	latRad := lat * math.Pi / 180.0
	n := math.Pow(2.0, float64(zoom))
	xtile := (lon + 180.0) / 360.0 * n
	ytile := (1.0 - math.Log(math.Tan(latRad)+1.0/math.Cos(latRad))/math.Pi) / 2.0 * n
	return xtile, ytile
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func main() {
	myApp := app.New()
	myApp.Settings().SetTheme(theme.DarkTheme())

	startZoom := 5
	startLat := 10.0
	startLon := 110.0
	mapWidget := NewTileMapWidget(startZoom, startLat, startLon)

	attributionLabel := widget.NewLabel("© OpenStreetMap contributors, © CARTO")
	attributionLabel.Alignment = fyne.TextAlignTrailing
	attributionLabel.TextStyle = fyne.TextStyle{Italic: true}
	scrollContainer := container.NewScroll(mapWidget)
	mapArea := container.NewMax(scrollContainer)
	content := container.NewBorder(nil, container.NewPadded(attributionLabel), nil, nil, mapArea)

	myWindow := myApp.NewWindow("Fyne Carto Map (Pure Go - Channel Fix)")
	if drv, ok := myApp.Driver().(desktop.Driver); ok {
		log.Println("Driver is desktop. Calling SetMaster().")
		myWindow.SetMaster()
		_ = drv
	} else {
		log.Println("Driver is NOT desktop.")
	}
	myWindow.SetContent(content)
	myWindow.Resize(fyne.NewSize(900, 700))
	myWindow.CenterOnScreen()
	myWindow.ShowAndRun()
}
