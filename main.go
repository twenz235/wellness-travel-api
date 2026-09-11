package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed data/*.json
var dataFS embed.FS

const (
	maxHorizonDays = 30
	maxTripDays    = 30
	apiVersion     = "v1"
	timezoneName   = "Asia/Bangkok"
	coverageMin    = 0.8
)

type Place struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Province         string  `json:"province"`
	Region           string  `json:"region"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
	CoordinateRole   string  `json:"coordinateRole"`
	PlaceType        string  `json:"placeType"`
	CampingAvailable *bool   `json:"campingAvailable"`
	SourceURL        string  `json:"sourceUrl"`
	ImageURL         string  `json:"imageUrl,omitempty"`
	ImageCredit      string  `json:"imageCredit,omitempty"`
	ImageLicense     string  `json:"imageLicense,omitempty"`
}

type Preferences struct {
	Temperature struct {
		MinC   float64 `json:"minC"`
		MaxC   float64 `json:"maxC"`
		Weight int     `json:"weight"`
	} `json:"temperature"`
	Rain struct {
		Preference string `json:"preference"`
		Weight     int    `json:"weight"`
	} `json:"rain"`
	Air struct {
		Weight int `json:"weight"`
	} `json:"air"`
}
type RecommendationRequest struct {
	Dates *struct {
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
	} `json:"dates"`
	TripDays       int            `json:"tripDays"`
	Period         string         `json:"period"`
	ScoringProfile string         `json:"scoringProfile"`
	Preferences    *Preferences   `json:"preferences"`
	Requirements   map[string]any `json:"requirements"`
}
type DetailRequest struct {
	RecommendationRequest
	PlaceID           string `json:"placeId"`
	SelectedStartDate string `json:"selectedStartDate"`
	SelectedEndDate   string `json:"selectedEndDate"`
	Mode              string `json:"mode"`
}
type observation struct {
	At          time.Time
	Temperature *float64
	Rain        *float64
	PM25        *float64
	SourceID    string
}
type seasonalRecord struct {
	PlaceID           string   `json:"placeId"`
	Year              int      `json:"year"`
	Month             int      `json:"month"`
	MonthKey          string   `json:"monthKey"`
	TemperatureMeanC  *float64 `json:"temperatureMeanC"`
	RainMeanMmPerHour *float64 `json:"rainMeanMmPerHour"`
	WeatherCoverage   float64  `json:"weatherCoverage"`
	WeatherHours      int      `json:"weatherObservedHours"`
	ExpectedWeather   int      `json:"weatherExpectedHours"`
	AirPM25Mean       *float64 `json:"airPm25MeanUgm3"`
	AirDailyAQIMean   *float64 `json:"airDailyAqiMean"`
	AirValidDays      int      `json:"airValidDays"`
	AirExpectedDays   int      `json:"airExpectedDays"`
	SourceIDs         []string `json:"sourceIds"`
}
type seas5Point struct {
	Month    string  `json:"month"`
	AnomalyK float64 `json:"anomalyK"`
	MeanC    float64 `json:"temperatureMeanC"`
}
type Coverage struct {
	ObservedWeightedRatio float64 `json:"observedWeightedRatio"`
	ScoredDays            int     `json:"scoredDays"`
	RequestedDays         int     `json:"requestedDays"`
}
type Factor struct {
	Factor         string   `json:"factor"`
	Score          *float64 `json:"score"`
	AvailableHours int      `json:"availableHours"`
	ExpectedHours  int      `json:"expectedHours"`
}
type Requirement struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
type Source struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	RetrievedAt string `json:"retrievedAt"`
	ValidFrom   string `json:"validFrom"`
	ValidTo     string `json:"validTo"`
	Resolution  string `json:"resolution"`
	SourceURL   string `json:"sourceUrl"`
}
type DailyDetail struct {
	Date             string   `json:"date"`
	TemperatureMean  *float64 `json:"temperatureMeanC,omitempty"`
	RainMeanMm       *float64 `json:"rainMeanMm,omitempty"`
	PM25Mean         *float64 `json:"pm25MeanUgm3,omitempty"`
	UsAQIPM25        *int     `json:"usAqiPm25,omitempty"`
	TemperatureScore *float64 `json:"temperatureScore,omitempty"`
	RainScore        *float64 `json:"rainScore,omitempty"`
	AirScore         *float64 `json:"airScore,omitempty"`
	ObservedHours    int      `json:"observedHours"`
	ExpectedHours    int      `json:"expectedHours"`
	AirObservedHours int      `json:"airObservedHours"`
	AirExpectedHours int      `json:"airExpectedHours"`
	Missing          []string `json:"missing,omitempty"`
}
type HourlyDetail struct {
	At               string   `json:"at"`
	Temperature      *float64 `json:"temperatureC,omitempty"`
	Rain             *float64 `json:"rainMm,omitempty"`
	AirDate          string   `json:"airDate"`
	TemperatureValid bool     `json:"temperatureValid"`
	RainValid        bool     `json:"rainValid"`
}
type SeasonalYear struct {
	Year             int      `json:"year"`
	TemperatureMeanC *float64 `json:"temperatureMeanC,omitempty"`
	RainMeanMm       *float64 `json:"rainMeanMm,omitempty"`
	AirDailyAqiMean  *float64 `json:"airDailyAqiMean,omitempty"`
	WeatherCoverage  float64  `json:"weatherCoverage"`
	AirValidDays     int      `json:"airValidDays"`
	AirExpectedDays  int      `json:"airExpectedDays"`
	SourceIDs        []string `json:"sourceIds"`
}
type YearOutlook struct {
	Month               string  `json:"month"`
	AnomalyK            float64 `json:"anomalyK"`
	BaselineDescription string  `json:"baselineDescription"`
	ScoreAdjusted       bool    `json:"scoreAdjusted"`
}
type Details struct {
	Daily         []DailyDetail  `json:"daily,omitempty"`
	Hourly        []HourlyDetail `json:"hourly,omitempty"`
	Sources       []Source       `json:"sources,omitempty"`
	SeasonalYears []SeasonalYear `json:"seasonalYears,omitempty"`
	YearOutlook   *YearOutlook   `json:"yearOutlook,omitempty"`
}
type Item struct {
	PlaceID      string           `json:"placeId"`
	Place        Place            `json:"place"`
	StartDate    string           `json:"startDate"`
	EndDate      string           `json:"endDate"`
	Score        *float64         `json:"score"`
	Metrics      *CardMetrics     `json:"metrics,omitempty"`
	Coverage     Coverage         `json:"coverage"`
	Factors      []Factor         `json:"factors"`
	Requirements []Requirement    `json:"requirements"`
	Reasons      []map[string]any `json:"reasons"`
	SourceIDs    []string         `json:"sourceIds"`
	Details      *Details         `json:"details,omitempty"`
}
type CardMetrics struct {
	TemperatureC  *float64 `json:"temperatureC,omitempty"`
	RainMmPerHour *float64 `json:"rainMmPerHour,omitempty"`
	UsAQIPM25     *int     `json:"usAqiPm25,omitempty"`
}
type Group struct {
	Mode   string `json:"mode"`
	Status string `json:"status"`
	Items  []Item `json:"items"`
}
type RecommendationResponse struct {
	RequestID      string         `json:"requestId"`
	ScoringVersion string         `json:"scoringVersion"`
	GeneratedAt    string         `json:"generatedAt"`
	Timezone       string         `json:"timezone"`
	Search         map[string]any `json:"search"`
	Groups         []Group        `json:"groups"`
	Warnings       []string       `json:"warnings"`
}
type rawHourly struct {
	Time        []string   `json:"time"`
	Temperature []*float64 `json:"temperature_2m"`
	Rain        []*float64 `json:"rain"`
	PM25        []*float64 `json:"pm2_5"`
}
type rawResponse struct {
	Hourly rawHourly `json:"hourly"`
}
type cacheEntry struct {
	Response  RecommendationResponse
	ExpiresAt time.Time
}
type server struct {
	places          []Place
	forecastWeather map[string][]observation
	forecastAir     map[string][]observation
	seasonal        map[string][]seasonalRecord
	seas5           map[string][]seas5Point
	sources         map[string]Source
	loc             *time.Location
	clock           func() time.Time
	fromSupabase    bool
	cache           map[string]cacheEntry
	cacheMu         sync.Mutex
}

func main() {
	s, err := loadServer()
	if err != nil {
		log.Fatal(err)
	}
	addr := getenv("PORT", "8080")
	log.Printf("wellness-travel-api listening on :%s (catalog=%s)", addr, map[bool]string{true: "supabase", false: "file"}[s.fromSupabase])
	gin.SetMode(gin.ReleaseMode)
	log.Fatal(newRouter(s).Run(":" + addr))
}

func loadServer() (*server, error) {
	loc, err := time.LoadLocation(timezoneName)
	if err != nil {
		return nil, err
	}
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	places, err := loadPlaces(filepath.Join(root, "data", "places.json"))
	if err != nil {
		return nil, err
	}
	weather, weatherSource, err := loadForecast(filepath.Join(root, "data", "forecast.json"), places, false, loc)
	if err != nil {
		return nil, err
	}
	air, airSource, err := loadForecast(filepath.Join(root, "data", "air-forecast.json"), places, true, loc)
	if err != nil {
		return nil, err
	}
	seasonal, err := loadSeasonal(filepath.Join(root, "data", "seasonal_month.json"))
	if err != nil {
		return nil, err
	}
	seas5, err := loadSeas5(filepath.Join(root, "data", "seas5.json"))
	if err != nil {
		return nil, err
	}
	sources := map[string]Source{weatherSource.ID: weatherSource, airSource.ID: airSource}
	sources["seas5"] = Source{ID: "seas5", Provider: "ECMWF", Model: "SEAS5", RetrievedAt: time.Now().UTC().Format(time.RFC3339), Resolution: "monthly", SourceURL: "https://seasonal-api.open-meteo.com/v1/seasonal"}
	for _, rs := range seasonal {
		for _, row := range rs {
			for _, id := range row.SourceIDs {
				if _, ok := sources[id]; !ok {
					sources[id] = Source{ID: id, Provider: "Open-Meteo", Model: "ERA5/CAMS archived", Resolution: "hourly", SourceURL: "https://open-meteo.com/en/docs"}
				}
			}
		}
	}
	s := &server{places: places, forecastWeather: weather, forecastAir: air, seasonal: seasonal, seas5: seas5, sources: sources, loc: loc, clock: time.Now, cache: make(map[string]cacheEntry)}
	if os.Getenv("SUPABASE_URL") != "" && os.Getenv("SUPABASE_SERVICE_ROLE_KEY") != "" {
		remote, e := fetchSupabasePlaces(context.Background(), os.Getenv("SUPABASE_URL"), os.Getenv("SUPABASE_SERVICE_ROLE_KEY"))
		if e != nil {
			return nil, fmt.Errorf("supabase catalog: %w", e)
		}
		s.places, s.fromSupabase = remote, true
	}
	return s, nil
}

var (
	handlerOnce   sync.Once
	handlerRouter http.Handler
	handlerErr    error
)

// Handler adapts the shared Gin router to Vercel's serverless request model.
// Initialization is cached for the lifetime of a warm function instance.
func Handler(w http.ResponseWriter, r *http.Request) {
	handlerOnce.Do(func() {
		var s *server
		s, handlerErr = loadServer()
		if handlerErr == nil {
			gin.SetMode(gin.ReleaseMode)
			handlerRouter = newRouter(s)
		}
	})
	if handlerErr != nil {
		http.Error(w, "server initialization failed", http.StatusInternalServerError)
		return
	}
	handlerRouter.ServeHTTP(w, r)
}

// newRouter is shared by the local binary and Vercel's Go Framework Preset.
// The domain handlers stay net/http-compatible so existing contract tests can
// exercise them directly while Gin owns routing, recovery, and CORS in the
// running application.
func newRouter(s *server) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), ginCORS())
	router.GET("/healthz", gin.WrapF(s.health))
	router.GET("/v1/capabilities", gin.WrapF(s.capabilities))
	router.GET("/v1/places", gin.WrapF(s.placesHandler))
	router.POST("/v1/recommendations", gin.WrapF(s.recommendations))
	router.POST("/v1/recommendations/detail", gin.WrapF(s.recommendationDetail))
	return router
}

func readData(path string) ([]byte, error) {
	b, fileErr := os.ReadFile(path)
	if fileErr == nil {
		return b, nil
	}

	// Keep callers using normal filesystem paths while resolving the same
	// relative data path from the embedded FS in serverless deployments.
	rel := filepath.ToSlash(path)
	if i := strings.Index(rel, "data/"); i >= 0 {
		rel = rel[i:]
	} else {
		rel = strings.TrimPrefix(rel, "./")
	}
	if b, embedErr := dataFS.ReadFile(rel); embedErr == nil {
		return b, nil
	}
	return nil, fileErr
}

func loadPlaces(path string) ([]Place, error) {
	b, e := readData(path)
	if e != nil {
		return nil, e
	}
	var w struct {
		Places []struct {
			ID        string  `json:"id"`
			Name      string  `json:"name_th"`
			Province  string  `json:"province"`
			Region    string  `json:"region_group"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Role      string  `json:"coordinate_semantics"`
			Type      string  `json:"type"`
			Camping   *bool   `json:"camping_available"`
			URL       string  `json:"official_park_url"`
		} `json:"places"`
	}
	if e = json.Unmarshal(b, &w); e != nil {
		return nil, e
	}
	out := make([]Place, 0, len(w.Places))
	for _, p := range w.Places {
		out = append(out, Place{ID: p.ID, Name: p.Name, Province: p.Province, Region: p.Region, Latitude: p.Latitude, Longitude: p.Longitude, CoordinateRole: p.Role, PlaceType: p.Type, CampingAvailable: p.Camping, SourceURL: p.URL})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func loadForecast(path string, places []Place, air bool, loc *time.Location) (map[string][]observation, Source, error) {
	b, e := readData(path)
	if e != nil {
		return nil, Source{}, e
	}
	var rows []rawResponse
	if e = json.Unmarshal(b, &rows); e != nil {
		return nil, Source{}, e
	}
	if len(rows) != len(places) {
		return nil, Source{}, fmt.Errorf("%s: expected %d places, got %d", path, len(places), len(rows))
	}
	h := sha256.Sum256(b)
	id := fmt.Sprintf("%s-%s", map[bool]string{true: "air_forecast", false: "forecast"}[air], hex.EncodeToString(h[:])[:12])
	model, url := map[bool]string{true: "CAMS Global PM2.5 forecast", false: "Open-Meteo forecast"}[air], map[bool]string{true: "https://air-quality-api.open-meteo.com/v1/air-quality", false: "https://api.open-meteo.com/v1/forecast"}[air]
	out := make(map[string][]observation)
	var first, last time.Time
	for i, row := range rows {
		for j, stamp := range row.Hourly.Time {
			at, er := parseLocalTime(stamp, loc)
			if er != nil {
				return nil, Source{}, er
			}
			o := observation{At: at, SourceID: id}
			if air {
				if j < len(row.Hourly.PM25) {
					o.PM25 = row.Hourly.PM25[j]
				}
			} else {
				if j < len(row.Hourly.Temperature) {
					o.Temperature = row.Hourly.Temperature[j]
				}
				if j < len(row.Hourly.Rain) {
					o.Rain = row.Hourly.Rain[j]
				}
			}
			out[places[i].ID] = append(out[places[i].ID], o)
			if first.IsZero() || at.Before(first) {
				first = at
			}
			if at.After(last) {
				last = at
			}
		}
	}
	retrieved := time.Now().UTC()
	if st, er := os.Stat(path); er == nil {
		retrieved = st.ModTime().UTC()
	}
	return out, Source{ID: id, Provider: "Open-Meteo", Model: model, RetrievedAt: retrieved.Format(time.RFC3339), ValidFrom: first.UTC().Format(time.RFC3339), ValidTo: last.UTC().Format(time.RFC3339), Resolution: "1 hour", SourceURL: url}, nil
}
func loadSeasonal(path string) (map[string][]seasonalRecord, error) {
	b, e := readData(path)
	if e != nil {
		return nil, e
	}
	var w struct {
		Records []seasonalRecord `json:"records"`
	}
	if e = json.Unmarshal(b, &w); e != nil {
		return nil, e
	}
	out := map[string][]seasonalRecord{}
	for _, r := range w.Records {
		out[r.PlaceID] = append(out[r.PlaceID], r)
	}
	return out, nil
}
func loadSeas5(path string) (map[string][]seas5Point, error) {
	b, e := readData(path)
	if e != nil {
		return nil, e
	}
	var w struct {
		Records []struct {
			PlaceID string       `json:"placeId"`
			Months  []seas5Point `json:"months"`
		} `json:"records"`
	}
	if e = json.Unmarshal(b, &w); e != nil {
		return nil, e
	}
	out := map[string][]seas5Point{}
	for _, r := range w.Records {
		out[r.PlaceID] = r.Months
	}
	return out, nil
}
func fetchSupabasePlaces(ctx context.Context, base, key string) ([]Place, error) {
	u := strings.TrimRight(base, "/") + "/rest/v1/place?select=id,name,province,region,latitude,longitude,coordinate_role,place_type,camping_available,source_url,image_url,image_credit,image_license&order=id"
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("apikey", key)
	req.Header.Set("Authorization", "Bearer "+key)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("status %s: %s", res.Status, string(body))
	}
	var rows []struct {
		ID        string  `json:"id"`
		Name      string  `json:"name"`
		Province  string  `json:"province"`
		Region    string  `json:"region"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Role      string  `json:"coordinate_role"`
		Type      string  `json:"place_type"`
		Camping   *bool   `json:"camping_available"`
		Source    string  `json:"source_url"`
		Image     string  `json:"image_url"`
		Credit    string  `json:"image_credit"`
		License   string  `json:"image_license"`
	}
	if e = json.NewDecoder(res.Body).Decode(&rows); e != nil {
		return nil, e
	}
	out := make([]Place, 0, len(rows))
	for _, p := range rows {
		out = append(out, Place{ID: p.ID, Name: p.Name, Province: p.Province, Region: p.Region, Latitude: p.Latitude, Longitude: p.Longitude, CoordinateRole: p.Role, PlaceType: p.Type, CampingAvailable: p.Camping, SourceURL: p.Source, ImageURL: p.Image, ImageCredit: p.Credit, ImageLicense: p.License})
	}
	return out, nil
}

// fetchURL is used by the refresh/import boundary. A timeout must fail closed
// so a provider outage cannot be mistaken for a complete weather dataset.
func fetchURL(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream status %s", res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 16<<20))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := corsAllowOrigin(r.Header.Get("Origin")); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ginCORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		if origin := corsAllowOrigin(c.GetHeader("Origin")); origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func corsAllowOrigin(origin string) string {
	configured := getenv("CORS_ORIGIN", "http://localhost:3000,http://127.0.0.1:3000,http://localhost:13001,http://127.0.0.1:13001")
	allowed := strings.Split(configured, ",")
	if origin == "" {
		if len(allowed) == 0 {
			return ""
		}
		return strings.TrimSpace(allowed[0])
	}
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == origin {
			return origin
		}
	}
	return ""
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": apiVersion, "catalogSource": map[bool]string{true: "supabase", false: "file"}[s.fromSupabase], "forecastPlaces": len(s.forecastWeather), "seasonalPlaces": len(s.seasonal)})
}
func (s *server) capabilities(w http.ResponseWriter, r *http.Request) {
	now := s.clock().In(s.loc)
	writeJSON(w, http.StatusOK, map[string]any{"serverTime": now.Format(time.RFC3339), "timezone": timezoneName, "scoringVersion": "v1", "datePolicy": map[string]any{"minStartDate": dateOnly(now.AddDate(0, 0, 1)), "maxEndDate": dateOnly(now.AddDate(0, 0, maxHorizonDays)), "maxHorizonDays": maxHorizonDays, "maxTripDays": maxTripDays}, "supportedPeriods": []string{"day", "night", "all"}, "defaultTripDays": 2, "datasetCoverage": map[string]any{"forecastHours": forecastHours(s.forecastWeather), "seasonalRecords": seasonalRecords(s.seasonal), "places": len(s.places)}})
}
func (s *server) placesHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"places": s.places, "count": len(s.places)})
}

func (s *server) recommendations(w http.ResponseWriter, r *http.Request) {
	var req RecommendationRequest
	if e := decodeJSON(r, &req); e != nil {
		writeError(w, http.StatusBadRequest, "malformed_json")
		return
	}
	dates, flex, e := s.validateRequest(&req)
	if e != nil {
		writeError(w, http.StatusUnprocessableEntity, e.Error())
		return
	}
	key := requestCacheKey(req, dates, flex)
	if cached, ok := s.cacheGet(key); ok {
		cached.Warnings = append(cached.Warnings, "served_from_cache")
		writeJSON(w, http.StatusOK, cached)
		return
	}
	resp := s.makeResponse(req, dates, flex)
	s.cachePut(key, resp, cacheTTL(flex))
	writeJSON(w, http.StatusOK, resp)
}
func (s *server) recommendationDetail(w http.ResponseWriter, r *http.Request) {
	var in DetailRequest
	if e := decodeJSON(r, &in); e != nil {
		writeError(w, http.StatusBadRequest, "malformed_json")
		return
	}
	if in.PlaceID == "" {
		writeError(w, http.StatusUnprocessableEntity, "place_id_required")
		return
	}
	if in.SelectedStartDate != "" || in.SelectedEndDate != "" {
		if in.SelectedStartDate == "" || in.SelectedEndDate == "" {
			writeError(w, http.StatusUnprocessableEntity, "selected_dates_must_be_both")
			return
		}
		// A Seasonal card carries its selected best window for display context,
		// but that window must not switch the detail request into Forecast mode.
		// Seasonal scoring still searches the server-owned 30-day window.
		if in.Mode != "seasonal" {
			in.Dates = &struct {
				StartDate string `json:"startDate"`
				EndDate   string `json:"endDate"`
			}{in.SelectedStartDate, in.SelectedEndDate}
		}
	}
	dates, flex, e := s.validateRequest(&in.RecommendationRequest)
	if e != nil {
		writeError(w, http.StatusUnprocessableEntity, e.Error())
		return
	}
	if in.Mode == "forecast" && flex || in.Mode == "seasonal" && !flex {
		writeError(w, http.StatusUnprocessableEntity, "mode_dates_mismatch")
		return
	}
	resp := s.makeResponse(in.RecommendationRequest, dates, flex)
	for gi := range resp.Groups {
		for ii := range resp.Groups[gi].Items {
			if resp.Groups[gi].Items[ii].PlaceID == in.PlaceID {
				if flex {
					resp.Groups[gi].Items[ii].Details = s.seasonalDetails(resp.Groups[gi].Items[ii], in.RecommendationRequest, dates)
				} else {
					resp.Groups[gi].Items[ii].Details = s.forecastDetails(resp.Groups[gi].Items[ii], in.RecommendationRequest, dates)
				}
				writeJSON(w, http.StatusOK, resp)
				return
			}
		}
	}
	writeError(w, http.StatusNotFound, "place_not_found")
}

func systemBaselinePreferences() *Preferences {
	p := &Preferences{}
	p.Temperature.MinC = 25
	p.Temperature.MaxC = 30
	p.Temperature.Weight = 1
	p.Rain.Preference = "dry"
	p.Rain.Weight = 1
	p.Air.Weight = 1
	return p
}

func (s *server) validateRequest(req *RecommendationRequest) ([]time.Time, bool, error) {
	if req.ScoringProfile == "" {
		return nil, false, errors.New("scoring_profile_required")
	}
	switch req.ScoringProfile {
	case "system":
		// System recommendations always use the declared baseline, even if a
		// caller accidentally includes user preferences in the request.
		req.Preferences = systemBaselinePreferences()
	case "user":
		// User recommendations validate the supplied profile below.
	default:
		return nil, false, errors.New("invalid_scoring_profile")
	}
	if req.Period != "all" && req.Period != "day" && req.Period != "night" {
		return nil, false, errors.New("invalid_period")
	}
	if req.TripDays < 1 || req.TripDays > maxTripDays {
		return nil, false, errors.New("trip_days_out_of_range")
	}
	if req.Preferences == nil {
		return nil, false, errors.New("preferences_required")
	}
	if !finite(req.Preferences.Temperature.MinC) || !finite(req.Preferences.Temperature.MaxC) || req.Preferences.Temperature.MinC > req.Preferences.Temperature.MaxC {
		return nil, false, errors.New("temperature_range_invalid")
	}
	if req.Preferences.Rain.Preference != "dry" && req.Preferences.Rain.Preference != "light" && req.Preferences.Rain.Preference != "moderate" {
		return nil, false, errors.New("invalid_rain_preference")
	}
	if !validWeight(req.Preferences.Temperature.Weight) || !validWeight(req.Preferences.Rain.Weight) || !validWeight(req.Preferences.Air.Weight) {
		return nil, false, errors.New("invalid_weight")
	}
	if pt, ok := req.Requirements["placeType"]; ok && pt != nil {
		if str, ok := pt.(string); !ok || str != "national_park" {
			return nil, false, errors.New("invalid_place_type")
		}
	}
	if req.Dates == nil {
		return nil, true, nil
	}
	if req.Dates.StartDate == "" || req.Dates.EndDate == "" {
		return nil, false, errors.New("dates_must_be_both_or_null")
	}
	start, e := time.ParseInLocation("2006-01-02", req.Dates.StartDate, s.loc)
	if e != nil {
		return nil, false, errors.New("invalid_start_date")
	}
	end, e := time.ParseInLocation("2006-01-02", req.Dates.EndDate, s.loc)
	if e != nil {
		return nil, false, errors.New("invalid_end_date")
	}
	now := s.clock().In(s.loc)
	min := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, s.loc)
	max := time.Date(now.Year(), now.Month(), now.Day()+maxHorizonDays, 0, 0, 0, 0, s.loc)
	if start.Before(min) || end.After(max) {
		return nil, false, errors.New("date_out_of_30_day_horizon")
	}
	if end.Before(start) {
		return nil, false, errors.New("end_before_start")
	}
	duration := daysBetween(start, end)
	if duration > maxTripDays {
		return nil, false, errors.New("trip_days_out_of_range")
	}
	if duration == 1 && req.Period == "night" {
		return nil, false, errors.New("single_day_night_not_supported")
	}
	return []time.Time{start, end}, false, nil
}

func (s *server) makeResponse(req RecommendationRequest, dates []time.Time, flex bool) RecommendationResponse {
	start := dateOnly(s.clock().In(s.loc).AddDate(0, 0, 1))
	end := dateOnly(s.clock().In(s.loc).AddDate(0, 0, maxHorizonDays))
	trip := req.TripDays
	mode := "seasonal"
	if flex {
		if trip == 0 {
			trip = 2
		}
	} else {
		mode = "forecast"
		start = dateOnly(dates[0])
		end = dateOnly(dates[1])
		trip = daysBetween(dates[0], dates[1])
	}
	items := make([]Item, 0, len(s.places))
	for _, p := range s.places {
		if flex {
			items = append(items, s.seasonalItem(p, req, start, end))
		} else {
			items = append(items, s.forecastItem(p, req, dates[0], dates[1]))
		}
	}
	if flex {
		sortItems(items)
		status := "matched"
		for _, it := range items {
			itemState := itemStatus(it)
			if itemState == "not_matched" {
				status = "not_matched"
				break
			}
			if itemState == "incomplete" {
				status = "incomplete"
			}
		}
		return s.response(req, dates, true, start, end, trip, []Group{{Mode: mode, Status: status, Items: items}}, nil)
	}
	groups := map[string][]Item{"matched": {}, "incomplete": {}, "not_matched": {}}
	for _, it := range items {
		groups[itemStatus(it)] = append(groups[itemStatus(it)], it)
	}
	for _, xs := range groups {
		sortItems(xs)
	}
	return s.response(req, dates, false, start, end, trip, []Group{{Mode: mode, Status: "matched", Items: groups["matched"]}, {Mode: mode, Status: "incomplete", Items: groups["incomplete"]}, {Mode: mode, Status: "not_matched", Items: groups["not_matched"]}}, nil)
}
func (s *server) response(req RecommendationRequest, dates []time.Time, flex bool, start, end string, trip int, groups []Group, warnings []string) RecommendationResponse {
	if warnings == nil {
		warnings = []string{}
	}
	if flex {
		warnings = append(warnings, "seasonal uses ERA5 2016-2025 and CAMS archived forecasts 2023-2025; it is a historical trend, not a daily forecast")
	}
	return RecommendationResponse{RequestID: fmt.Sprintf("local-%d", s.clock().UnixNano()), ScoringVersion: "v1", GeneratedAt: s.clock().UTC().Format(time.RFC3339), Timezone: timezoneName, Search: map[string]any{"kind": map[bool]string{true: "flexible", false: "vacation"}[flex], "startDate": start, "endDate": end, "tripDays": trip, "mode": map[bool]string{true: "seasonal", false: "forecast"}[flex], "scoringProfile": req.ScoringProfile, "selectedDates": dates}, Groups: groups, Warnings: warnings}
}

func (s *server) forecastItem(p Place, req RecommendationRequest, start, end time.Time) Item {
	hours := selectedHours(start, end, req.Period)
	weather := s.forecastWeather[p.ID]
	air := dailyAirRecords(s.forecastAir[p.ID])
	var temps, rains, airs []float64
	tn, rn, an := 0, 0, 0
	missing := []string{}
	for _, h := range hours {
		found := false
		for _, o := range weather {
			if o.At.Equal(h) {
				found = true
				if o.Temperature != nil {
					temps = append(temps, *o.Temperature)
					tn++
				}
				if o.Rain != nil {
					rains = append(rains, *o.Rain)
					rn++
				}
				break
			}
		}
		if !found {
			missing = appendUnique(missing, "weather:"+h.Format("2006-01-02T15:00"))
		}
		if d, ok := air[h.Format("2006-01-02")]; ok && d.Valid {
			airs = append(airs, float64(d.AQI))
			an++
		} else {
			missing = appendUnique(missing, "air:"+h.Format("2006-01-02"))
		}
	}
	expected := len(hours)
	factors := []Factor{{"temperature", factorAverage(temps, tn, expected, func(v float64) float64 {
		return scoreTemp(v, req.Preferences.Temperature.MinC, req.Preferences.Temperature.MaxC)
	}), tn, expected}, {"rain", factorAverage(rains, rn, expected, func(v float64) float64 { return scoreRain(v, req.Preferences.Rain.Preference) }), rn, expected}, {"air", factorAverage(airs, an, expected, func(v float64) float64 { return scoreDustAQI(v) }), an, expected}}
	score := weightedFactors(factors[0].Score, factors[1].Score, factors[2].Score, req.Preferences)
	reqs := s.requirementsFor(p, req, hours, air, weather)
	reasons := []map[string]any{}
	if len(missing) > 0 {
		reasons = append(reasons, map[string]any{"code": "missing_observations", "fields": missing})
	}
	for _, r := range reqs {
		if r.Status != "passed" {
			reasons = append(reasons, map[string]any{"code": "requirement_" + r.Status, "key": r.Key, "message": r.Reason})
		}
	}
	if len(missing) > 0 {
		reasons = append(reasons, map[string]any{"code": "coverage_rule", "message": "แต่ละปัจจัยต้องมีข้อมูลอย่างน้อย 80% ของชั่วโมงที่เลือก"})
	}
	metrics := cardMetricsFromForecast(factors, temps, rains, airs)
	return Item{PlaceID: p.ID, Place: p, StartDate: dateOnly(start), EndDate: dateOnly(end), Score: score, Metrics: metrics, Coverage: Coverage{weightedCoverage(factors, req.Preferences), daysBetween(start, end), daysBetween(start, end)}, Factors: factors, Requirements: reqs, Reasons: reasons, SourceIDs: sourcesFor(weather, airRows(s.forecastAir[p.ID]))}
}

func cardMetricsFromForecast(factors []Factor, temps, rains, airs []float64) *CardMetrics {
	if len(factors) < 3 {
		return nil
	}
	m := &CardMetrics{}
	if factors[0].Score != nil && len(temps) > 0 {
		m.TemperatureC = ptr(average(temps))
	}
	if factors[1].Score != nil && len(rains) > 0 {
		m.RainMmPerHour = ptr(average(rains))
	}
	if factors[2].Score != nil && len(airs) > 0 {
		aqi := int(math.Round(average(airs)))
		m.UsAQIPM25 = &aqi
	}
	if m.TemperatureC == nil && m.RainMmPerHour == nil && m.UsAQIPM25 == nil {
		return nil
	}
	return m
}
func (s *server) seasonalItem(p Place, req RecommendationRequest, start, end string) Item {
	trip := req.TripDays
	if trip == 0 {
		trip = 2
	}
	windowStart, errStart := time.ParseInLocation("2006-01-02", start, s.loc)
	windowEnd, errEnd := time.ParseInLocation("2006-01-02", end, s.loc)
	if errStart != nil || errEnd != nil || windowEnd.Before(windowStart) {
		return s.seasonalItemForPeriod(p, req, start, end)
	}
	lastStart := windowEnd.AddDate(0, 0, -(trip - 1))
	if lastStart.Before(windowStart) {
		lastStart = windowStart
	}
	var best *Item
	for candidate := windowStart; !candidate.After(lastStart); candidate = candidate.AddDate(0, 0, 1) {
		candidateEnd := candidate.AddDate(0, 0, trip-1)
		item := s.seasonalItemForPeriod(p, req, dateOnly(candidate), dateOnly(candidateEnd))
		if best == nil || betterSeasonalItem(item, *best) {
			copy := item
			best = &copy
		}
	}
	if best != nil {
		return *best
	}
	return s.seasonalItemForPeriod(p, req, start, end)
}

func betterSeasonalItem(candidate, current Item) bool {
	if candidate.Score == nil {
		return false
	}
	if current.Score == nil {
		return true
	}
	if *candidate.Score != *current.Score {
		return *candidate.Score > *current.Score
	}
	if candidate.Coverage.ObservedWeightedRatio != current.Coverage.ObservedWeightedRatio {
		return candidate.Coverage.ObservedWeightedRatio > current.Coverage.ObservedWeightedRatio
	}
	return candidate.StartDate < current.StartDate
}

func (s *server) seasonalItemForPeriod(p Place, req RecommendationRequest, start, end string) Item {
	month := parseMonth(start)
	rows := []seasonalRecord{}
	for _, r := range s.seasonal[p.ID] {
		if r.Month == month {
			rows = append(rows, r)
		}
	}
	temps, rains, airs := []float64{}, []float64{}, []float64{}
	tempValues, rainValues, airValues := []float64{}, []float64{}, []float64{}
	ty, ry, ay := 0, 0, 0
	ids := []string{}
	for _, r := range rows {
		if r.TemperatureMeanC != nil && r.WeatherCoverage >= coverageMin {
			tempValues = append(tempValues, *r.TemperatureMeanC)
			temps = append(temps, scoreTemp(*r.TemperatureMeanC, req.Preferences.Temperature.MinC, req.Preferences.Temperature.MaxC))
			ty++
		}
		if r.RainMeanMmPerHour != nil && r.WeatherCoverage >= coverageMin {
			rainValues = append(rainValues, *r.RainMeanMmPerHour)
			rains = append(rains, scoreRain(*r.RainMeanMmPerHour, req.Preferences.Rain.Preference))
			ry++
		}
		if r.AirDailyAQIMean != nil && r.AirExpectedDays > 0 && float64(r.AirValidDays)/float64(r.AirExpectedDays) >= coverageMin {
			airValues = append(airValues, *r.AirDailyAQIMean)
			airs = append(airs, scoreDustAQI(*r.AirDailyAQIMean))
			ay++
		}
		ids = appendUnique(ids, r.SourceIDs...)
	}
	factors := []Factor{{"temperature", avgPtr(temps), ty, 10}, {"rain", avgPtr(rains), ry, 10}, {"air", avgPtr(airs), ay, 3}}
	score := weightedFactors(factors[0].Score, factors[1].Score, factors[2].Score, req.Preferences)
	reasons := []map[string]any{{"code": "historical_seasonal", "message": "แนวโน้มจาก ERA5 10 ปีและ CAMS archived forecast 3 ปี ไม่ใช่การยืนยันอากาศรายวัน"}}
	if ty < 8 || ry < 8 || ay < 3 {
		reasons = append(reasons, map[string]any{"code": "seasonal_coverage_below_threshold", "message": "ต้องมีอากาศอย่างน้อย 8/10 ปีและฝุ่น 3/3 ปี"})
	}
	if len(s.seas5[p.ID]) > 0 {
		ids = appendUnique(ids, "seas5")
	}
	requirements := []Requirement{{Key: "placeType", Status: map[bool]string{true: "passed", false: "failed"}[p.PlaceType == "national_park"]}}
	for _, key := range []string{"maxTemperatureC", "maxRainMmPerHour", "maxDailyUsAqiPm25"} {
		if _, ok := numberRequirement(req.Requirements, key); ok {
			requirements = append(requirements, Requirement{Key: key, Status: "unknown", Reason: "seasonal_is_not_a_daily_requirement_check"})
			reasons = append(reasons, map[string]any{"code": "seasonal_requirement_unknown", "key": key, "message": "Seasonal แสดงแนวโน้มย้อนหลัง จึงไม่ยืนยันข้อจำเป็นรายวัน"})
		}
	}
	metrics := cardMetricsFromSeasonal(factors, tempValues, rainValues, airValues)
	return Item{PlaceID: p.ID, Place: p, StartDate: start, EndDate: end, Score: score, Metrics: metrics, Coverage: Coverage{weightedCoverage(factors, req.Preferences), 0, 0}, Factors: factors, Requirements: requirements, Reasons: reasons, SourceIDs: ids}
}

func cardMetricsFromSeasonal(factors []Factor, temps, rains, airs []float64) *CardMetrics {
	if len(factors) < 3 {
		return nil
	}
	m := &CardMetrics{}
	if factors[0].Score != nil && len(temps) > 0 {
		m.TemperatureC = ptr(average(temps))
	}
	if factors[1].Score != nil && len(rains) > 0 {
		m.RainMmPerHour = ptr(average(rains))
	}
	if factors[2].Score != nil && len(airs) > 0 {
		aqi := int(math.Round(average(airs)))
		m.UsAQIPM25 = &aqi
	}
	if m.TemperatureC == nil && m.RainMmPerHour == nil && m.UsAQIPM25 == nil {
		return nil
	}
	return m
}

func (s *server) requirementsFor(p Place, req RecommendationRequest, hours []time.Time, air map[string]dailyAirRecord, weather []observation) []Requirement {
	out := []Requirement{{Key: "placeType", Status: map[bool]string{true: "passed", false: "failed"}[p.PlaceType == "national_park"]}}
	if p.PlaceType != "national_park" {
		out[0].Reason = "place_type_not_national_park"
	}
	if v, ok := numberRequirement(req.Requirements, "maxTemperatureC"); ok {
		st, rs := thresholdWeather(hours, weather, func(o observation) *float64 { return o.Temperature }, func(x float64) bool { return x > v })
		out = append(out, Requirement{"maxTemperatureC", st, rs})
	}
	if v, ok := numberRequirement(req.Requirements, "maxRainMmPerHour"); ok {
		st, rs := thresholdWeather(hours, weather, func(o observation) *float64 { return o.Rain }, func(x float64) bool { return x > v })
		out = append(out, Requirement{"maxRainMmPerHour", st, rs})
	}
	if v, ok := numberRequirement(req.Requirements, "maxDailyUsAqiPm25"); ok {
		seen := map[string]bool{}
		failed, unknown := false, false
		for _, h := range hours {
			d := h.Format("2006-01-02")
			if seen[d] {
				continue
			}
			seen[d] = true
			row, ok := air[d]
			if !ok || !row.Valid {
				unknown = true
				continue
			}
			if float64(row.AQI) > v {
				failed = true
			}
		}
		st, rs := "passed", ""
		if failed {
			st, rs = "failed", "daily_aqi_above_threshold"
		} else if unknown {
			st, rs = "unknown", "missing_daily_air"
		}
		out = append(out, Requirement{"maxDailyUsAqiPm25", st, rs})
	}
	return out
}
func thresholdWeather(hours []time.Time, rows []observation, value func(observation) *float64, fails func(float64) bool) (string, string) {
	found := false
	for _, h := range hours {
		for _, o := range rows {
			if o.At.Equal(h) {
				v := value(o)
				if v == nil {
					continue
				}
				found = true
				if fails(*v) {
					return "failed", "threshold_exceeded"
				}
				break
			}
		}
	}
	if !found {
		return "unknown", "missing_weather"
	}
	return "passed", ""
}
func (s *server) forecastDetails(item Item, req RecommendationRequest, dates []time.Time) *Details {
	if len(dates) != 2 {
		return &Details{}
	}
	hours := selectedHours(dates[0], dates[1], req.Period)
	weather := s.forecastWeather[item.PlaceID]
	air := dailyAirRecords(s.forecastAir[item.PlaceID])
	d := &Details{Hourly: []HourlyDetail{}, Sources: s.sourcesForIDs(item.SourceIDs), YearOutlook: s.outlook(item.PlaceID, item.StartDate)}
	for _, h := range hours {
		var t, r *float64
		for _, o := range weather {
			if o.At.Equal(h) {
				t = o.Temperature
				r = o.Rain
				break
			}
		}
		d.Hourly = append(d.Hourly, HourlyDetail{h.UTC().Format(time.RFC3339), t, r, h.Format("2006-01-02"), t != nil, r != nil})
	}
	for day := dates[0]; !day.After(dates[1]); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		var ts, rs []float64
		for _, h := range hours {
			if h.Format("2006-01-02") == key {
				for _, o := range weather {
					if o.At.Equal(h) {
						if o.Temperature != nil {
							ts = append(ts, *o.Temperature)
						}
						if o.Rain != nil {
							rs = append(rs, *o.Rain)
						}
					}
				}
			}
		}
		row := DailyDetail{Date: key, ObservedHours: len(ts), ExpectedHours: expectedHours(req.Period), AirExpectedHours: 24}
		if len(ts) > 0 {
			row.TemperatureMean = ptr(average(ts))
			row.RainMeanMm = ptr(average(rs))
			row.TemperatureScore = ptr(scoreTemp(*row.TemperatureMean, req.Preferences.Temperature.MinC, req.Preferences.Temperature.MaxC))
			row.RainScore = ptr(scoreRain(*row.RainMeanMm, req.Preferences.Rain.Preference))
		}
		if ar, ok := air[key]; ok && ar.Valid {
			row.PM25Mean = ptr(ar.Mean)
			row.UsAQIPM25 = ptrInt(ar.AQI)
			row.AirScore = ptr(scoreDustAQI(float64(ar.AQI)))
			row.AirObservedHours = ar.Observed
		} else {
			row.Missing = []string{"daily_air"}
		}
		d.Daily = append(d.Daily, row)
	}
	return d
}
func (s *server) seasonalDetails(item Item, req RecommendationRequest, dates []time.Time) *Details {
	d := &Details{Sources: s.sourcesForIDs(item.SourceIDs), YearOutlook: s.outlook(item.PlaceID, item.StartDate)}
	for _, r := range s.seasonal[item.PlaceID] {
		if r.Month == parseMonth(item.StartDate) {
			d.SeasonalYears = append(d.SeasonalYears, SeasonalYear{r.Year, r.TemperatureMeanC, r.RainMeanMmPerHour, r.AirDailyAQIMean, r.WeatherCoverage, r.AirValidDays, r.AirExpectedDays, r.SourceIDs})
		}
	}
	return d
}
func (s *server) outlook(placeID, start string) *YearOutlook {
	if len(start) < 7 {
		return nil
	}
	for _, x := range s.seas5[placeID] {
		if strings.HasPrefix(x.Month, start[:7]) {
			desc := "SEAS5 อุ่นกว่าค่าฐาน"
			if x.AnomalyK < 0 {
				desc = "SEAS5 เย็นกว่าค่าฐาน"
			}
			return &YearOutlook{x.Month, x.AnomalyK, desc, false}
		}
	}
	return nil
}

func scoreTemp(v, min, max float64) float64 {
	d := 0.0
	if v < min {
		d = min - v
	}
	if v > max {
		d = v - max
	}
	return math.Max(0, 100-10*d)
}
func scoreRain(v float64, pref string) float64 {
	cat := 0
	if v > 0 && v <= 2.5 {
		cat = 1
	} else if v > 2.5 && v <= 7.6 {
		cat = 2
	} else if v > 7.6 {
		cat = 3
	}
	return map[string][]float64{"dry": {100, 60, 20, 0}, "light": {80, 100, 40, 0}, "moderate": {60, 80, 100, 0}}[pref][cat]
}
func scoreDustAQI(v float64) float64 {
	knots := [][2]float64{{20, 100}, {70, 70}, {100, 50}, {150, 30}, {200, 0}}
	if v <= 20 {
		return 100
	}
	if v >= 200 {
		return 0
	}
	for i := 0; i < len(knots)-1; i++ {
		if v <= knots[i+1][0] {
			x0, y0 := knots[i][0], knots[i][1]
			x1, y1 := knots[i+1][0], knots[i+1][1]
			return y0 + (y1-y0)*(v-x0)/(x1-x0)
		}
	}
	return 0
}
func aqiPM25(v float64) *int {
	if !finite(v) || v < 0 {
		return nil
	}
	c := math.Floor(v*10) / 10
	bp := [][4]float64{{0, 9, 0, 50}, {9.1, 35.4, 51, 100}, {35.5, 55.4, 101, 150}, {55.5, 125.4, 151, 200}, {125.5, 225.4, 201, 300}, {225.5, 325.4, 301, 500}}
	for _, b := range bp {
		if c <= b[1] || b[2] == 301 {
			raw := (b[3]-b[2])*(c-b[0])/(b[1]-b[0]) + b[2]
			return ptrInt(int(math.Floor(raw + 0.5)))
		}
	}
	return nil
}
func weightedFactors(a, b, c *float64, p *Preferences) *float64 {
	vals := []float64{}
	ws := []int{}
	for i, v := range []*float64{a, b, c} {
		if v != nil {
			vals = append(vals, *v)
			ws = append(ws, []int{p.Temperature.Weight, p.Rain.Weight, p.Air.Weight}[i])
		}
	}
	return weighted(vals, ws)
}
func weighted(vals []float64, ws []int) *float64 {
	if len(vals) == 0 {
		return nil
	}
	sum, w := 0.0, 0
	for i, v := range vals {
		sum += v * float64(ws[i])
		w += ws[i]
	}
	return ptr(sum / float64(w))
}
func factorAverage(vals []float64, available, expected int, fn func(float64) float64) *float64 {
	if expected == 0 || available < ceil(float64(expected)*coverageMin) {
		return nil
	}
	scores := make([]float64, len(vals))
	for i, v := range vals {
		scores[i] = fn(v)
	}
	return ptr(average(scores))
}
func weightedCoverage(fs []Factor, p *Preferences) float64 {
	ws := []int{p.Temperature.Weight, p.Rain.Weight, p.Air.Weight}
	num, den := 0.0, 0.0
	for i, f := range fs {
		num += float64(f.AvailableHours * ws[i])
		den += float64(f.ExpectedHours * ws[i])
	}
	if den == 0 {
		return 0
	}
	return num / den
}
func avgPtr(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	return ptr(average(v))
}
func average(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	return sum / float64(len(v))
}
func itemStatus(it Item) string {
	for _, r := range it.Requirements {
		if r.Status == "failed" {
			return "not_matched"
		}
		if r.Status == "unknown" {
			return "incomplete"
		}
	}
	for _, f := range it.Factors {
		if f.Score == nil {
			return "incomplete"
		}
	}
	return "matched"
}
func sortItems(xs []Item) {
	sort.SliceStable(xs, func(i, j int) bool {
		if xs[i].Score == nil && xs[j].Score != nil {
			return false
		}
		if xs[i].Score != nil && xs[j].Score == nil {
			return true
		}
		if xs[i].Score != nil && xs[j].Score != nil && *xs[i].Score != *xs[j].Score {
			return *xs[i].Score > *xs[j].Score
		}
		if xs[i].Coverage.ObservedWeightedRatio != xs[j].Coverage.ObservedWeightedRatio {
			return xs[i].Coverage.ObservedWeightedRatio > xs[j].Coverage.ObservedWeightedRatio
		}
		return xs[i].PlaceID < xs[j].PlaceID
	})
}
func selectedHours(start, end time.Time, period string) []time.Time {
	out := []time.Time{}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		from, to := 0, 24
		if period == "day" {
			from, to = 6, 18
		}
		if period == "night" {
			from, to = 18, 24
		}
		for h := from; h < to; h++ {
			out = append(out, time.Date(day.Year(), day.Month(), day.Day(), h, 0, 0, 0, day.Location()))
		}
		if period == "night" {
			next := day.AddDate(0, 0, 1)
			for h := 0; h < 6; h++ {
				out = append(out, time.Date(next.Year(), next.Month(), next.Day(), h, 0, 0, 0, next.Location()))
			}
		}
	}
	return out
}
func expectedHours(p string) int {
	if p == "day" || p == "night" {
		return 12
	}
	return 24
}
func parseLocalTime(stamp string, loc *time.Location) (time.Time, error) {
	if t, e := time.ParseInLocation("2006-01-02T15:04", stamp, loc); e == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, stamp)
}
func dateOnly(t time.Time) string    { return t.Format("2006-01-02") }
func daysBetween(a, b time.Time) int { return int(b.Sub(a).Hours()/24) + 1 }
func parseMonth(s string) int {
	if len(s) < 7 {
		return 0
	}
	var m int
	fmt.Sscanf(s[5:7], "%d", &m)
	return m
}
func finite(v float64) bool  { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validWeight(v int) bool { return v >= 1 && v <= 3 }
func ceil(v float64) int     { return int(math.Ceil(v)) }
func ptr(v float64) *float64 { return &v }
func ptrInt(v int) *int      { return &v }
func appendUnique(xs []string, vals ...string) []string {
	seen := map[string]bool{}
	for _, x := range xs {
		seen[x] = true
	}
	for _, x := range vals {
		if x != "" && !seen[x] {
			xs = append(xs, x)
			seen[x] = true
		}
	}
	return xs
}
func sourcesFor(weather, air []observation) []string { return sourcesForRows(weather, air) }
func sourcesForRows(rows ...[]observation) []string {
	out := []string{}
	for _, rs := range rows {
		for _, o := range rs {
			out = appendUnique(out, o.SourceID)
		}
	}
	return out
}

func (s *server) sourcesForIDs(ids []string) []Source {
	out := []Source{}
	for _, id := range ids {
		if src, ok := s.sources[id]; ok {
			out = append(out, src)
		}
	}
	return out
}
func airRows(xs []observation) []observation { return xs }
func dailyAirRecords(rows []observation) map[string]dailyAirRecord {
	out := map[string]dailyAirRecord{}
	vals := map[string][]float64{}
	for _, o := range rows {
		if o.PM25 != nil && finite(*o.PM25) && *o.PM25 >= 0 {
			d := o.At.Format("2006-01-02")
			vals[d] = append(vals[d], *o.PM25)
		}
	}
	for d, xs := range vals {
		if len(xs) >= 20 {
			m := average(xs)
			if aq := aqiPM25(m); aq != nil {
				out[d] = dailyAirRecord{Mean: m, AQI: *aq, Valid: true, Observed: len(xs)}
			}
		} else {
			out[d] = dailyAirRecord{Observed: len(xs)}
		}
	}
	return out
}

type dailyAirRecord struct {
	Mean     float64
	AQI      int
	Valid    bool
	Observed int
}

func numberRequirement(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok && finite(f) && f >= 0
}
func forecastHours(m map[string][]observation) int {
	n := 0
	for _, x := range m {
		if len(x) > n {
			n = len(x)
		}
	}
	return n
}
func seasonalRecords(m map[string][]seasonalRecord) int {
	n := 0
	for _, x := range m {
		n += len(x)
	}
	return n
}
func requestCacheKey(req RecommendationRequest, dates []time.Time, flex bool) string {
	b, _ := json.Marshal(req)
	b = append(b, []byte(fmt.Sprint(dates, flex))...)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func cacheTTL(flex bool) time.Duration {
	if flex {
		return 24 * time.Hour
	}
	return time.Hour
}
func (s *server) cacheGet(k string) (RecommendationResponse, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	v, ok := s.cache[k]
	if !ok || !v.ExpiresAt.After(s.clock()) {
		if ok {
			delete(s.cache, k)
		}
		return RecommendationResponse{}, false
	}
	return v.Response, true
}
func (s *server) cachePut(k string, v RecommendationResponse, ttl time.Duration) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cache[k] = cacheEntry{v, s.clock().Add(ttl)}
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func decodeJSON(r *http.Request, v any) error {
	if r.Method != http.MethodPost {
		return errors.New("method_not_allowed")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code}})
}
