package desk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeocodeAndForecast(t *testing.T) {
	var gotForecastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/geo"):
			if r.URL.Query().Get("name") != "Amsterdam" || r.URL.Query().Get("count") != "1" {
				t.Errorf("geocode query = %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"results":[{"name":"Amsterdam","country":"The Netherlands","latitude":52.37403,"longitude":4.88969,"timezone":"Europe/Amsterdam"}]}`))
		case strings.HasPrefix(r.URL.Path, "/fc"):
			gotForecastQuery = r.URL.RawQuery
			w.Write([]byte(`{"timezone":"Europe/Amsterdam","current":{"time":"2026-10-31T11:00","interval":900,"temperature_2m":9.0,"apparent_temperature":6.7,"weather_code":3,"wind_speed_10m":10.4,"relative_humidity_2m":89},"daily":{"time":["2026-10-31","2026-11-01","2026-11-02"],"temperature_2m_max":[11.2,10.1,9.8],"temperature_2m_min":[5.1,4.0,3.3],"weather_code":[3,61,80],"precipitation_probability_max":[20,80,55]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	GeocodeURL, ForecastURL = srv.URL+"/geo", srv.URL+"/fc"
	t.Cleanup(func() {
		GeocodeURL, ForecastURL = "https://geocoding-api.open-meteo.com/v1/search", "https://api.open-meteo.com/v1/forecast"
	})

	p, err := Geocode(context.Background(), srv.Client(), "Amsterdam")
	if err != nil || p.Latitude < 52 || p.Timezone != "Europe/Amsterdam" {
		t.Fatalf("Geocode = %+v, %v", p, err)
	}
	w, err := Forecast(context.Background(), srv.Client(), p)
	if err != nil {
		t.Fatal(err)
	}
	if w.Temp != 9.0 || w.FeelsLike != 6.7 || w.Text != "overcast" || w.Humidity != 89 || w.Wind != 10.4 {
		t.Fatalf("current = %+v", w)
	}
	if len(w.Days) != 3 || w.Days[1].Text != "rain" || w.Days[1].RainProb != 80 || w.Days[2].Max != 9.8 || w.Days[0].Date != "2026-10-31" {
		t.Fatalf("days = %+v", w.Days)
	}
	for _, want := range []string{"latitude=52.3740", "longitude=4.8897", "forecast_days=3", "timezone=auto", "weather_code"} {
		if !strings.Contains(gotForecastQuery, want) {
			t.Errorf("forecast query missing %q: %s", want, gotForecastQuery)
		}
	}
	if _, err := Geocode(context.Background(), srv.Client(), "  "); err == nil {
		t.Fatal("empty location must error")
	}
}

func TestWeatherText(t *testing.T) {
	cases := map[int]string{0: "clear", 2: "partly cloudy", 45: "fog", 53: "drizzle", 63: "rain", 73: "snow", 81: "rain showers", 95: "thunderstorm", 99: "thunderstorm with hail", 42: "unknown"}
	for code, want := range cases {
		if got := WeatherText(code); got != want {
			t.Errorf("WeatherText(%d) = %q; want %q", code, got, want)
		}
	}
}
