package desk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Open-Meteo: free, no key, generous limits. Base URLs are vars for tests.
var (
	GeocodeURL  = "https://geocoding-api.open-meteo.com/v1/search"
	ForecastURL = "https://api.open-meteo.com/v1/forecast"
)

type Place struct {
	Name      string  `json:"name"`
	Country   string  `json:"country"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
}

type WeatherDay struct {
	Date     string  `json:"date"`
	Code     int     `json:"code"`
	Text     string  `json:"text"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	RainProb int     `json:"rain_prob"`
}

type Weather struct {
	Place     Place        `json:"place"`
	Time      string       `json:"time"`
	Temp      float64      `json:"temp"`
	FeelsLike float64      `json:"feels_like"`
	Code      int          `json:"code"`
	Text      string       `json:"text"`
	Wind      float64      `json:"wind_kmh"`
	Humidity  int          `json:"humidity"`
	Days      []WeatherDay `json:"days"`
}

// Geocode resolves a place name to coordinates (first match).
func Geocode(ctx context.Context, hc *http.Client, name string) (Place, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Place{}, fmt.Errorf("empty location")
	}
	q := url.Values{"name": {name}, "count": {"1"}, "language": {"en"}, "format": {"json"}}
	var out struct {
		Results []Place `json:"results"`
	}
	if err := getJSON(ctx, hc, GeocodeURL+"?"+q.Encode(), nil, &out); err != nil {
		return Place{}, fmt.Errorf("geocode: %w", err)
	}
	if len(out.Results) == 0 {
		return Place{}, fmt.Errorf("geocode: no match for %q", name)
	}
	return out.Results[0], nil
}

// Forecast fetches current conditions and a 3-day outlook for a place.
func Forecast(ctx context.Context, hc *http.Client, p Place) (Weather, error) {
	q := url.Values{
		"latitude":      {fmt.Sprintf("%.4f", p.Latitude)},
		"longitude":     {fmt.Sprintf("%.4f", p.Longitude)},
		"current":       {"temperature_2m,apparent_temperature,weather_code,wind_speed_10m,relative_humidity_2m"},
		"daily":         {"temperature_2m_max,temperature_2m_min,weather_code,precipitation_probability_max"},
		"timezone":      {"auto"},
		"forecast_days": {"3"},
	}
	var raw struct {
		Current struct {
			Time        string  `json:"time"`
			Temperature float64 `json:"temperature_2m"`
			Apparent    float64 `json:"apparent_temperature"`
			Code        int     `json:"weather_code"`
			Wind        float64 `json:"wind_speed_10m"`
			Humidity    int     `json:"relative_humidity_2m"`
		} `json:"current"`
		Daily struct {
			Time     []string  `json:"time"`
			Max      []float64 `json:"temperature_2m_max"`
			Min      []float64 `json:"temperature_2m_min"`
			Code     []int     `json:"weather_code"`
			RainProb []int     `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	if err := getJSON(ctx, hc, ForecastURL+"?"+q.Encode(), nil, &raw); err != nil {
		return Weather{}, fmt.Errorf("forecast: %w", err)
	}
	w := Weather{
		Place: p, Time: raw.Current.Time, Temp: raw.Current.Temperature, FeelsLike: raw.Current.Apparent,
		Code: raw.Current.Code, Text: WeatherText(raw.Current.Code), Wind: raw.Current.Wind, Humidity: raw.Current.Humidity,
	}
	for i := range raw.Daily.Time {
		d := WeatherDay{Date: raw.Daily.Time[i]}
		if i < len(raw.Daily.Code) {
			d.Code = raw.Daily.Code[i]
			d.Text = WeatherText(d.Code)
		}
		if i < len(raw.Daily.Min) {
			d.Min = raw.Daily.Min[i]
		}
		if i < len(raw.Daily.Max) {
			d.Max = raw.Daily.Max[i]
		}
		if i < len(raw.Daily.RainProb) {
			d.RainProb = raw.Daily.RainProb[i]
		}
		w.Days = append(w.Days, d)
	}
	return w, nil
}

// WeatherText maps WMO weather codes (what Open-Meteo returns) to words.
func WeatherText(code int) string {
	switch {
	case code == 0:
		return "clear"
	case code == 1:
		return "mainly clear"
	case code == 2:
		return "partly cloudy"
	case code == 3:
		return "overcast"
	case code == 45 || code == 48:
		return "fog"
	case code >= 51 && code <= 55:
		return "drizzle"
	case code == 56 || code == 57:
		return "freezing drizzle"
	case code >= 61 && code <= 65:
		return "rain"
	case code == 66 || code == 67:
		return "freezing rain"
	case code >= 71 && code <= 75:
		return "snow"
	case code == 77:
		return "snow grains"
	case code >= 80 && code <= 82:
		return "rain showers"
	case code == 85 || code == 86:
		return "snow showers"
	case code == 95:
		return "thunderstorm"
	case code == 96 || code == 99:
		return "thunderstorm with hail"
	}
	return "unknown"
}

// getJSON GETs a URL (optional headers) and decodes JSON; non-2xx is an error.
func getJSON(ctx context.Context, hc *http.Client, u string, hdr map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: HTTP %d", resp.Request.URL.Host, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
