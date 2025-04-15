package main

import (
	"context"
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
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	minZoom         = 8
	maxZoom         = 18
	tileSize        = 256
	tileResultBuf   = 64
	yourUserAgent   = "MyFyneMapApp/0.2 (contact@example.com)"
	markerRadius    = 5    // Pixel radius for the marker circle
	markerHitRadius = 10.0 // Pixel radius for click detection
	fetchTimeout    = 15 * time.Second
	sendTimeout     = 5 * time.Second
	mapTileSize     = 256 // Tile size expected

	mapboxUsername    = "thanhdat19"
	mapboxStyleID     = "cm9is30la00sx01qua6xa2b7s"
	mapboxAccessToken = "pk.eyJ1IjoidGhhbmhkYXQxOSIsImEiOiJjbTlpcHgycXgwMjcwMmpxMTRybXczamMwIn0.NLpIdFMutAPECag8yVaERA" // WARNING: Consider loading from config/env
)

var (
	httpClient = &http.Client{Timeout: fetchTimeout}
)

type TileCoord struct {
	Z, X, Y int
}

type TileResult struct {
	Coord TileCoord
	Image image.Image
	Error error
}

type MapMarker struct {
	ID   string
	Lat  float64
	Lon  float64
	Name string
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
	markers        []*MapMarker
	parentWindow   fyne.Window
}

func NewTileMapWidget(startZoom int, startLat, startLon float64, parentWin fyne.Window) *TileMapWidget {
	m := &TileMapWidget{
		zoom:           startZoom,
		centerLat:      startLat,
		centerLon:      startLon,
		imageDataCache: make(map[TileCoord]image.Image),
		tileFetching:   make(map[TileCoord]bool),
		resultChan:     make(chan TileResult, tileResultBuf),
		stopChan:       make(chan struct{}),
		markers:        make([]*MapMarker, 0),
		parentWindow:   parentWin,
	}
	m.ExtendBaseWidget(m)
	return m
}

func (m *TileMapWidget) AddMarkers(newMarkers ...*MapMarker) {
	if len(newMarkers) == 0 {
		return
	}

	validMarkers := make([]*MapMarker, 0, len(newMarkers))
	for _, marker := range newMarkers {
		if marker != nil {
			validMarkers = append(validMarkers, marker)
		}
	}

	if len(validMarkers) == 0 {
		return
	}

	m.mu.Lock()
	m.markers = append(m.markers, validMarkers...)
	m.mu.Unlock()
	m.Refresh()
}

func (m *TileMapWidget) latLonToScreenXY(markerLat, markerLon float64) (float32, float32) {
	m.mu.RLock()
	zoom := m.zoom
	centerLat := m.centerLat
	centerLon := m.centerLon
	w := m.width
	h := m.height
	m.mu.RUnlock()

	if w <= 0 || h <= 0 {
		return -1, -1
	}

	n := math.Pow(2.0, float64(zoom))

	markerPxX := ((markerLon + 180.0) / 360.0) * n * tileSize
	markerPxY := (1.0 - math.Log(math.Tan(markerLat*math.Pi/180.0)+1.0/math.Cos(markerLat*math.Pi/180.0))/math.Pi) / 2.0 * n * tileSize

	centerPxX := ((centerLon + 180.0) / 360.0) * n * tileSize
	centerPxY := (1.0 - math.Log(math.Tan(centerLat*math.Pi/180.0)+1.0/math.Cos(centerLat*math.Pi/180.0))/math.Pi) / 2.0 * n * tileSize

	offsetX := markerPxX - centerPxX
	offsetY := markerPxY - centerPxY

	screenX := (w / 2.0) + float32(offsetX)
	screenY := (h / 2.0) + float32(offsetY)

	return screenX, screenY
}

func (m *TileMapWidget) CreateRenderer() fyne.WidgetRenderer {
	return &tileMapRenderer{
		mapWidget:     m,
		canvasTiles:   make(map[TileCoord]*canvas.Image),
		canvasMarkers: make(map[*MapMarker]fyne.CanvasObject),
	}
}

func (m *TileMapWidget) Destroy() {
	close(m.stopChan)
}

func (m *TileMapWidget) Dragged(e *fyne.DragEvent) {
	m.mu.RLock()
	currentZoom := m.zoom
	currentLat := m.centerLat
	currentLon := m.centerLon
	m.mu.RUnlock()

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
	dy := e.Scrolled.DY
	if dy == 0 {
		return
	}

	m.mu.Lock()
	// oldZoom := m.zoom
	zoomChanged := false

	if dy < 0 { // Zoom in
		if m.zoom < maxZoom {
			m.zoom++
			zoomChanged = true
			log.Println("Zoom In -> Zoom", m.zoom)
		}
	} else { // Zoom out
		if m.zoom > minZoom {
			m.zoom--
			zoomChanged = true
			log.Println("Zoom Out -> Zoom", m.zoom)
		}
	}
	m.mu.Unlock()

	if zoomChanged {
		m.Refresh()
	}
}

func (m *TileMapWidget) Tapped(e *fyne.PointEvent) {
	m.mu.RLock()
	markersToCheck := make([]*MapMarker, len(m.markers))
	copy(markersToCheck, m.markers)
	m.mu.RUnlock()

	for _, marker := range markersToCheck {
		markerX, markerY := m.latLonToScreenXY(marker.Lat, marker.Lon)

		dx := e.Position.X - markerX
		dy := e.Position.Y - markerY
		distSq := dx*dx + dy*dy

		if distSq <= (markerHitRadius * markerHitRadius) {
			log.Printf("Tapped Marker: %s (%.4f, %.4f)", marker.Name, marker.Lat, marker.Lon)
			if m.parentWindow != nil {
				info := fmt.Sprintf("Marker: %s\nLat: %.6f\nLon: %.6f", marker.Name, marker.Lat, marker.Lon)
				dialog.ShowInformation("Marker Info", info, m.parentWindow)
			} else {
				log.Println("Warning: Cannot show marker dialog, parent window reference is nil")
			}
			return
		}
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

// --- Renderer ---

type tileMapRenderer struct {
	mapWidget     *TileMapWidget
	objects       []fyne.CanvasObject
	canvasTiles   map[TileCoord]*canvas.Image
	canvasMarkers map[*MapMarker]fyne.CanvasObject
}

func (r *tileMapRenderer) Layout(size fyne.Size) {
	r.mapWidget.mu.Lock()
	r.mapWidget.width = size.Width
	r.mapWidget.height = size.Height
	r.mapWidget.mu.Unlock()
	// Positioning is handled in Refresh
}

func (r *tileMapRenderer) MinSize() fyne.Size {
	return fyne.NewSize(tileSize, tileSize)
}

func (r *tileMapRenderer) Refresh() {
	r.processTileResults() // Handle completed fetches

	r.mapWidget.mu.RLock()
	zoom := r.mapWidget.zoom
	centerLat := r.mapWidget.centerLat
	centerLon := r.mapWidget.centerLon
	width := r.mapWidget.width
	height := r.mapWidget.height
	currentMarkers := make([]*MapMarker, len(r.mapWidget.markers))
	copy(currentMarkers, r.mapWidget.markers)
	r.mapWidget.mu.RUnlock()

	if width <= 0 || height <= 0 {
		return // Avoid rendering in zero area
	}

	// --- Tiles ---
	visibleTiles := r.calculateRequiredTiles(zoom, centerLat, centerLon, width, height)
	neededCoords := make([]TileCoord, 0, len(visibleTiles))
	activeCanvasTiles := make(map[TileCoord]bool)
	currentTileObjects := make([]fyne.CanvasObject, 0, len(visibleTiles))

	r.mapWidget.mu.Lock() // Lock for cache/fetching maps access
	for _, coord := range visibleTiles {
		imgData, dataFound := r.mapWidget.imageDataCache[coord]
		if dataFound {
			canvasImg, canvasFound := r.canvasTiles[coord]
			if !canvasFound {
				canvasImg = canvas.NewImageFromImage(imgData)
				if canvasImg == nil {
					log.Printf("Error: Failed to create canvas image from cached data for tile %v", coord)
					delete(r.mapWidget.imageDataCache, coord) // Remove potentially bad data
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
			currentTileObjects = append(currentTileObjects, canvasImg)
			activeCanvasTiles[coord] = true
		} else {
			if !r.mapWidget.tileFetching[coord] {
				neededCoords = append(neededCoords, coord)
				r.mapWidget.tileFetching[coord] = true
			}
		}
	}
	r.mapWidget.mu.Unlock()

	// Cleanup unused tile canvas objects
	for coord, img := range r.canvasTiles {
		if !activeCanvasTiles[coord] {
			img.Hide()
			delete(r.canvasTiles, coord)
		}
	}

	// --- Markers ---
	currentMarkerObjects := make([]fyne.CanvasObject, 0, len(currentMarkers))
	activeCanvasMarkers := make(map[*MapMarker]bool)

	for _, marker := range currentMarkers {
		screenX, screenY := r.mapWidget.latLonToScreenXY(marker.Lat, marker.Lon)

		// Optionally skip rendering if marker is way off-screen
		// if screenX < -markerRadius || screenX > width+markerRadius || screenY < -markerRadius || screenY > height+markerRadius {
		// 	if canvasObj, exists := r.canvasMarkers[marker]; exists {
		// 		canvasObj.Hide()
		// 	}
		// 	continue
		// }

		canvasObj, exists := r.canvasMarkers[marker]
		var circle *canvas.Circle

		if !exists {
			circle = canvas.NewCircle(markerColor)
			circle.Resize(fyne.NewSize(markerRadius*2, markerRadius*2))
			r.canvasMarkers[marker] = circle
			canvasObj = circle
		} else {
			// Assuming it's always a circle if it exists
			circle = canvasObj.(*canvas.Circle)
		}

		circle.Move(fyne.NewPos(screenX-markerRadius, screenY-markerRadius))
		circle.Show()
		currentMarkerObjects = append(currentMarkerObjects, canvasObj)
		activeCanvasMarkers[marker] = true
	}

	// Cleanup unused marker canvas objects
	for marker, obj := range r.canvasMarkers {
		if !activeCanvasMarkers[marker] {
			obj.Hide()
			delete(r.canvasMarkers, marker)
		}
	}

	// Combine tiles and markers (markers on top)
	r.objects = append(currentTileObjects, currentMarkerObjects...)

	// Fetch needed tiles asynchronously
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
				if result.Image.Bounds().Dx() > 0 && result.Image.Bounds().Dy() > 0 {
					r.mapWidget.imageDataCache[result.Coord] = result.Image
				} else {
					log.Printf("Warning: Received invalid image for tile %v (zero dimensions)", result.Coord)
				}
			} else if result.Error != nil {
				// Log fetch errors unless it's a common 404
				if result.Error.Error() != "tile not found (404)" {
					log.Printf("Error fetching tile %v: %v", result.Coord, result.Error)
				}
			} else {
				log.Printf("Warning: Received nil image and nil error for tile %v", result.Coord)
			}
			r.mapWidget.mu.Unlock()

		default:
			// No more results waiting
			return
		}
	}
}

func (r *tileMapRenderer) calculateRequiredTiles(zoom int, lat, lon float64, w, h float32) []TileCoord {
	centerX, centerY := latLonToTileXY(lat, lon, zoom)
	tilesX := int(math.Ceil(float64(w)/tileSize)) + 2 // Buffer
	tilesY := int(math.Ceil(float64(h)/tileSize)) + 2 // Buffer

	startX := int(math.Floor(centerX - float64(tilesX)/2.0))
	startY := int(math.Floor(centerY - float64(tilesY)/2.0))

	tiles := make([]TileCoord, 0, tilesX*tilesY)
	maxTile := int(math.Pow(2, float64(zoom))) - 1

	for x := startX; x < startX+tilesX; x++ {
		for y := startY; y < startY+tilesY; y++ {
			// Clamp Y coord
			if y < 0 || y > maxTile {
				continue
			}

			// Wrap X coord
			wrappedX := x
			if maxTile >= 0 {
				nWrap := maxTile + 1
				wrappedX = (x%nWrap + nWrap) % nWrap // Handles negative x
			} else {
				wrappedX = 0 // Zoom 0 case
			}

			tiles = append(tiles, TileCoord{Z: zoom, X: wrappedX, Y: y})
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
	fetchSuccessful := false

	defer func() {
		if !fetchSuccessful {
			r.clearFetchingStatus(coord) // Ensure status is cleared on error/panic/cancellation
		}
		if rec := recover(); rec != nil {
			log.Printf("Panic recovered in fetchTileDataAsync for %v: %v", coord, rec)
			result.Error = fmt.Errorf("panic during fetch: %v", rec)
			// Attempt to send error back, but don't block indefinitely
			select {
			case r.mapWidget.resultChan <- result:
			case <-time.After(100 * time.Millisecond):
			case <-r.mapWidget.stopChan:
			}
		}
	}()

	select {
	case <-r.mapWidget.stopChan:
		result.Error = fmt.Errorf("fetch cancelled")
		// Do not send to channel here, defer handles status cleanup
		return
	default:
		// Proceed with fetch
	}

	url := fmt.Sprintf("https://api.mapbox.com/styles/v1/%s/%s/tiles/%d/%d/%d/%d?access_token=%s",
		mapboxUsername,
		mapboxStyleID,
		mapTileSize, // Use constant for tile size (e.g., 256)
		coord.Z,
		coord.X,
		coord.Y,
		mapboxAccessToken,
	)

	fmt.Println("Fetching tile:", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		result.Error = fmt.Errorf("creating request failed: %w", err)
		r.sendResult(result)
		return
	}
	req.Header.Set("User-Agent", yourUserAgent)

	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := httpClient.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("http timeout for %s", url)
		} else if ctx.Err() == context.Canceled {
			result.Error = fmt.Errorf("http request cancelled")
		} else {
			result.Error = fmt.Errorf("http request failed for %s: %w", url, err)
		}
		r.sendResult(result)
		return
	}

	if resp.StatusCode == http.StatusNotFound {
		result.Error = fmt.Errorf("tile not found (404)")
		r.sendResult(result)
		return
	}
	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Errorf("http status %s for %s", resp.Status, url)
		r.sendResult(result)
		return
	}

	imgData, err := png.Decode(resp.Body)
	if err != nil {
		result.Error = fmt.Errorf("decoding png failed for %s: %w", url, err)
		r.sendResult(result)
		return
	}

	if imgData == nil || imgData.Bounds().Dx() <= 0 || imgData.Bounds().Dy() <= 0 {
		result.Error = fmt.Errorf("decoded image invalid (nil or zero size) for %s", url)
		r.sendResult(result)
		return
	}

	result.Image = imgData
	fetchSuccessful = true // Mark success before sending
	r.sendResult(result)
}

func (r *tileMapRenderer) sendResult(result TileResult) {
	select {
	case r.mapWidget.resultChan <- result:
		// Successfully sent
	case <-r.mapWidget.stopChan:
		log.Printf("Sending cancelled for tile %v result (widget stopped)", result.Coord)
	case <-time.After(sendTimeout):
		log.Printf("Timeout sending result for tile %v to channel. Discarding.", result.Coord)
		// If sending timed out, the fetch status might still be set.
		// Clear it regardless of whether the fetch succeeded or failed,
		// because the successful result was lost.
		r.clearFetchingStatus(result.Coord)
	}
}

func (r *tileMapRenderer) clearFetchingStatus(coord TileCoord) {
	r.mapWidget.mu.Lock()
	delete(r.mapWidget.tileFetching, coord)
	r.mapWidget.mu.Unlock()
}

func (r *tileMapRenderer) Objects() []fyne.CanvasObject {
	r.mapWidget.mu.RLock()
	// Return a shallow copy for safety
	objs := make([]fyne.CanvasObject, len(r.objects))
	copy(objs, r.objects)
	r.mapWidget.mu.RUnlock()
	return objs
}

func (r *tileMapRenderer) Destroy() {
	// Cleanup resources if needed, though Fyne handles CanvasObjects
	log.Println("TileMapRenderer Destroy called")
}

func latLonToTileXY(lat, lon float64, zoom int) (float64, float64) {
	latRad := lat * math.Pi / 180.0
	n := math.Pow(2.0, float64(zoom))
	xtile := (lon + 180.0) / 360.0 * n
	ytile := (1.0 - math.Log(math.Tan(latRad)+1.0/math.Cos(latRad))/math.Pi) / 2.0 * n
	return xtile, ytile
}

// --- Main Application ---

func main() {
	myApp := app.New()
	myApp.Settings().SetTheme(theme.DarkTheme())

	myWindow := myApp.NewWindow("Fyne Carto Map with Clickable Marker")

	startZoom := 12
	startLat := 10.7769  // Ho Chi Minh City Latitude
	startLon := 106.7009 // Ho Chi Minh City Longitude

	mapWidget := NewTileMapWidget(startZoom, startLat, startLon, myWindow)

	hcmcMarker := &MapMarker{
		Lat:  startLat,
		Lon:  startLon,
		Name: "Ho Chi Minh City",
		ID:   "HCMC",
	}
	nearbyMarker := &MapMarker{
		Lat:  startLat + 0.02,
		Lon:  startLon + 0.01,
		Name: "Nearby Place",
		ID:   "NEARBY",
	}

	mapWidget.AddMarkers(hcmcMarker, nearbyMarker)

	log.Println("Added multiple markers.")

	attributionLabel := widget.NewLabel("© OpenStreetMap contributors, © CARTO")
	attributionLabel.Alignment = fyne.TextAlignTrailing
	attributionLabel.TextStyle = fyne.TextStyle{Italic: true}

	mapArea := container.NewStack(mapWidget)
	content := container.NewBorder(nil, container.NewPadded(attributionLabel), nil, nil, mapArea)

	myWindow.SetContent(content)
	myWindow.Resize(fyne.NewSize(900, 700))
	myWindow.CenterOnScreen()
	myWindow.ShowAndRun()

	log.Println("Application exiting.")
}
