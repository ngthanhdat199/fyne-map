package main

// Convert lat/lon to pixel in equirectangular projection
// func LatLngToPixel(lat, lon float64, width, height int) (x, y int) {
// 	x = int((lon + 180.0) * (float64(width) / 360.0))
// 	y = int((90.0 - lat) * (float64(height) / 180.0))
// 	return
// }

// // Crop 256x256 centered at (x, y)
// func Crop256Centered(img image.Image, x, y int) image.Image {
// 	rect := image.Rect(x-128, y-128, x+128, y+128)

// 	// Clamp to image bounds
// 	bounds := img.Bounds()
// 	if rect.Min.X < bounds.Min.X {
// 		rect = rect.Add(image.Pt(bounds.Min.X-rect.Min.X, 0))
// 	}
// 	if rect.Min.Y < bounds.Min.Y {
// 		rect = rect.Add(image.Pt(0, bounds.Min.Y-rect.Min.Y))
// 	}
// 	if rect.Max.X > bounds.Max.X {
// 		rect = rect.Add(image.Pt(bounds.Max.X-rect.Max.X, 0))
// 	}
// 	if rect.Max.Y > bounds.Max.Y {
// 		rect = rect.Add(image.Pt(0, bounds.Max.Y-rect.Max.Y))
// 	}

// 	cropped := image.NewRGBA(image.Rect(0, 0, 256, 256))
// 	draw.Draw(cropped, cropped.Bounds(), img, rect.Min, draw.Src)
// 	return cropped
// }

// func main() {
// 	// Open map image (equirectangular world map)
// 	file, err := os.Open("worldmap.png") // You must provide this image
// 	// file, err := os.Open("blue-map.png") // You must provide this image

// 	if err != nil {
// 		log.Fatalf("Failed to open image: %v", err)
// 	}
// 	defer file.Close()

// 	img, _, err := image.Decode(file)
// 	if err != nil {
// 		log.Fatalf("Failed to decode image: %v", err)
// 	}

// 	width := img.Bounds().Dx()
// 	height := img.Bounds().Dy()

// 	// Input lat/lon
// 	// 10.7769, 106.7009

// 	lat := 10.7769 // example: Eiffel Tower
// 	lon := 106.7009

// 	x, y := LatLngToPixel(lat, lon, width, height)

// 	fmt.Printf("Lat/Lng (%.4f, %.4f) -> Pixel (%d, %d)\n", lat, lon, x, y)

// 	cropped := Crop256Centered(img, x, y)

// 	// Save to file
// 	outFile, err := os.Create("output_crop.jpg")
// 	if err != nil {
// 		log.Fatalf("Failed to create output file: %v", err)
// 	}
// 	defer outFile.Close()

// 	err = jpeg.Encode(outFile, cropped, &jpeg.Options{Quality: 90})
// 	if err != nil {
// 		log.Fatalf("Failed to encode JPEG: %v", err)
// 	}

// 	fmt.Println("Cropped 256x256 image saved as output_crop.jpg")
// }

// func mainTest() {
// 	coords := []onmap.Coord{
// 		{42.1, 19.1},             // Bar
// 		{55.755833, 37.617222},   // Moscow
// 		{41.9097306, 12.2558141}, // Rome
// 		{-31.952222, 115.858889}, // Perth
// 		{42.441286, 19.262892},   // Podgorica
// 		{38.615925, -27.226598},  // Azores
// 		{45.4628329, 9.1076924},  // Milano
// 		{43.7800607, 11.170928},  // Florence
// 		{37.7775, -122.416389},   // San Francisco
// 	}

// 	m := onmap.Pins(coords, onmap.StandardCrop)
// 	f, err := os.Create("out.png")
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	defer f.Close()
// 	if err := png.Encode(f, m); err != nil {
// 		log.Fatal(err)
// 	}
// }
