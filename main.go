package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// HTML template with geolocation JavaScript
const geoHTML = `
<!DOCTYPE html>
<html>
<head>
    <title>Geolocation</title>
    <style>
        body { font-family: Arial, sans-serif; text-align: center; padding: 20px; }
        button { padding: 10px 20px; font-size: 16px; }
        #status { margin: 20px; font-size: 18px; }
    </style>
</head>
<body>
    <h1>Geolocation Access</h1>
    <button onclick="getLocation()">Get My Location</button>
    <div id="status">Click the button to get your location</div>

    <script>
    function getLocation() {
        document.getElementById("status").innerHTML = "Requesting location...";
        
        if (navigator.geolocation) {
            navigator.geolocation.getCurrentPosition(
                function(position) {
                    var lat = position.coords.latitude;
                    var lon = position.coords.longitude;
                    document.getElementById("status").innerHTML = 
                        "Location found!<br>Latitude: " + lat + "<br>Longitude: " + lon;
                    
                    // Send to API endpoint
                    fetch('/location', {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/json',
                        },
                        body: JSON.stringify({
                            latitude: lat,
                            longitude: lon
                        }),
                    })
                    .then(response => response.json())
                    .then(data => {
                        document.getElementById("status").innerHTML += "<br>Location saved successfully!";
                    })
                    .catch((error) => {
                        document.getElementById("status").innerHTML += "<br>Error saving location: " + error;
                    });
                }, 
                function(error) {
                    let errorMsg = "Error getting location: ";
                    switch(error.code) {
                        case error.PERMISSION_DENIED:
                            errorMsg += "Permission denied";
                            break;
                        case error.POSITION_UNAVAILABLE:
                            errorMsg += "Position unavailable";
                            break;
                        case error.TIMEOUT:
                            errorMsg += "Timeout";
                            break;
                    }
                    document.getElementById("status").innerHTML = errorMsg;
                }
            );
        } else {
            document.getElementById("status").innerHTML = "Geolocation is not supported by this browser";
        }
    }
    </script>
</body>
</html>
`

// Location data structure
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func main() {
	// Create a new Fyne app
	a := app.New()
	w := a.NewWindow("Location Example")

	// Create a label to show output
	output := widget.NewLabel("Location will be displayed here")

	// Create button to start web server and open browser
	getLocationBtn := widget.NewButton("Open Browser for Location", func() {
		// Create temporary HTML file
		tempDir, err := os.MkdirTemp("", "fyne-map")
		if err != nil {
			output.SetText(fmt.Sprintf("Error creating temp dir: %v", err))
			return
		}
		htmlPath := filepath.Join(tempDir, "geolocation.html")
		err = os.WriteFile(htmlPath, []byte(geoHTML), 0644)
		if err != nil {
			output.SetText(fmt.Sprintf("Error creating HTML file: %v", err))
			return
		}

		// Start a local web server
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, htmlPath)
		})

		// Handle location data from JavaScript
		http.HandleFunc("/location", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
				return
			}

			var loc Location
			err := json.NewDecoder(r.Body).Decode(&loc)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			// Update the output in the Fyne app
			output.SetText(fmt.Sprintf("Location received:\nLatitude: %f\nLongitude: %f",
				loc.Latitude, loc.Longitude))

			// Send response
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		})

		// Start server in a goroutine
		go func() {
			serverAddr := "localhost:8080"
			output.SetText(fmt.Sprintf("Starting server at http://%s...", serverAddr))
			err := http.ListenAndServe(serverAddr, nil)
			if err != nil {
				output.SetText(fmt.Sprintf("Server error: %v", err))
			}
		}()

		// Open browser
		url := "http://localhost:8080"
		var cmd *exec.Cmd

		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default: // Linux and others
			cmd = exec.Command("xdg-open", url)
		}

		err = cmd.Start()
		if err != nil {
			output.SetText(fmt.Sprintf("Error opening browser: %v", err))
		}
	})

	// Button to use manually entered location
	latEntry := widget.NewEntry()
	latEntry.SetPlaceHolder("Enter latitude")

	lonEntry := widget.NewEntry()
	lonEntry.SetPlaceHolder("Enter longitude")

	// Layout
	w.SetContent(container.NewVBox(
		widget.NewLabel("Get Location Using Browser:"),
		getLocationBtn,
		widget.NewLabel("\nStatus:"),
		output,
	))

	// Show window
	w.Resize(fyne.NewSize(400, 400))
	w.ShowAndRun()
}
