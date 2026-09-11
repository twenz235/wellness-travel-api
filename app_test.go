package wellnesstravel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testServer(t *testing.T) *server {
	t.Helper()
	loc, err := time.LoadLocation(timezoneName)
	if err != nil {
		t.Fatal(err)
	}
	places, err := loadPlaces(filepath.Join("data", "places.json"))
	if err != nil {
		t.Fatal(err)
	}
	weather, ws, err := loadForecast(filepath.Join("data", "forecast.json"), places, false, loc)
	if err != nil {
		t.Fatal(err)
	}
	air, as, err := loadForecast(filepath.Join("data", "air-forecast.json"), places, true, loc)
	if err != nil {
		t.Fatal(err)
	}
	seasonal, err := loadSeasonal(filepath.Join("data", "seasonal_month.json"))
	if err != nil {
		t.Fatal(err)
	}
	seas5, err := loadSeas5(filepath.Join("data", "seas5.json"))
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]Source{ws.ID: ws, as.ID: as}
	for _, rows := range seasonal {
		for _, row := range rows {
			for _, id := range row.SourceIDs {
				if _, ok := sources[id]; !ok {
					sources[id] = Source{ID: id, Provider: "Open-Meteo"}
				}
			}
		}
	}
	fixed := time.Date(2026, 9, 11, 8, 0, 0, 0, loc)
	return &server{places: places, forecastWeather: weather, forecastAir: air, seasonal: seasonal, seas5: seas5, sources: sources, loc: loc, clock: func() time.Time { return fixed }, cache: map[string]cacheEntry{}}
}

func validRequest() RecommendationRequest {
	var p Preferences
	p.Temperature.MinC, p.Temperature.MaxC, p.Temperature.Weight = 20, 26, 2
	p.Rain.Preference, p.Rain.Weight = "light", 1
	p.Air.Weight = 3
	return RecommendationRequest{TripDays: 2, Period: "all", ScoringProfile: "user", Preferences: &p, Requirements: map[string]any{"placeType": "national_park"}}
}
func explicitDates(req *RecommendationRequest, start, end string) {
	req.Dates = &struct {
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
	}{start, end}
}
func TestProductionDatasetLoaded(t *testing.T) {
	s := testServer(t)
	if len(s.places) != 24 || len(s.forecastWeather) != 24 || len(s.forecastAir) != 24 || seasonalRecords(s.seasonal) != 2880 {
		t.Fatalf("dataset dimensions places=%d forecast=%d air=%d seasonal=%d", len(s.places), len(s.forecastWeather), len(s.forecastAir), seasonalRecords(s.seasonal))
	}
	if len(s.seasonal["park-01"]) != 120 {
		t.Fatalf("expected 120 monthly records, got %d", len(s.seasonal["park-01"]))
	}
}

func TestEmbeddedDataFallbackMatchesServerlessPath(t *testing.T) {
	places, err := loadPlaces(filepath.Join("/var/task", "data", "places.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 24 {
		t.Fatalf("embedded catalog places=%d, want 24", len(places))
	}
}

func TestFlexibleSeasonalUsesHistoricalYears(t *testing.T) {
	s := testServer(t)
	req := validRequest()
	dates, flex, err := s.validateRequest(&req)
	if err != nil || !flex || dates != nil {
		t.Fatalf("validation dates=%v flex=%v err=%v", dates, flex, err)
	}
	resp := s.makeResponse(req, dates, flex)
	if len(resp.Groups) != 1 || len(resp.Groups[0].Items) != 24 {
		t.Fatalf("expected 24 seasonal items, got %+v", resp.Groups)
	}
	item := resp.Groups[0].Items[0]
	if item.Score == nil || len(item.SourceIDs) < 10 || item.Requirements[0].Status != "passed" {
		t.Fatalf("seasonal item missing score/provenance/requirement: %+v", item)
	}
	if item.Metrics == nil || item.Metrics.TemperatureC == nil || item.Metrics.RainMmPerHour == nil || item.Metrics.UsAQIPM25 == nil {
		t.Fatalf("seasonal item missing card metrics: %+v", item.Metrics)
	}
	start, _ := time.Parse("2006-01-02", item.StartDate)
	end, _ := time.Parse("2006-01-02", item.EndDate)
	if int(end.Sub(start).Hours()/24)+1 != 2 {
		t.Fatalf("flexible result should expose a 2-day trip window, got %s..%s", item.StartDate, item.EndDate)
	}
	if item.Reasons[0]["code"] != "historical_seasonal" {
		t.Fatalf("unexpected seasonal reason: %+v", item.Reasons)
	}
}
func TestFlexibleSeasonalWindowIsBestPerPlaceWithin30Days(t *testing.T) {
	s := testServer(t)
	req := validRequest()
	resp := s.makeResponse(req, nil, true)
	clockDay := s.clock().In(s.loc)
	horizonStart := time.Date(clockDay.Year(), clockDay.Month(), clockDay.Day()+1, 0, 0, 0, 0, s.loc)
	horizonEnd := time.Date(clockDay.Year(), clockDay.Month(), clockDay.Day()+maxHorizonDays, 0, 0, 0, 0, s.loc)
	for _, item := range resp.Groups[0].Items {
		start, err := time.ParseInLocation("2006-01-02", item.StartDate, s.loc)
		if err != nil {
			t.Fatalf("place %s returned invalid start date %q: %v", item.PlaceID, item.StartDate, err)
		}
		end, err := time.ParseInLocation("2006-01-02", item.EndDate, s.loc)
		if err != nil {
			t.Fatalf("place %s returned invalid end date %q: %v", item.PlaceID, item.EndDate, err)
		}
		if start.Before(horizonStart) || end.After(horizonEnd) || daysBetween(start, end) != req.TripDays {
			t.Fatalf("place %s returned window outside 30-day trip policy: %s..%s", item.PlaceID, item.StartDate, item.EndDate)
		}
		place := item.Place
		lastStart := horizonEnd.AddDate(0, 0, -(req.TripDays - 1))
		for candidate := horizonStart; !candidate.After(lastStart); candidate = candidate.AddDate(0, 0, 1) {
			candidateItem := s.seasonalItemForPeriod(place, req, dateOnly(candidate), dateOnly(candidate.AddDate(0, 0, req.TripDays-1)))
			if betterSeasonalItem(candidateItem, item) {
				t.Fatalf("place %s did not return its best 30-day window: got %s..%s, better candidate %s..%s", item.PlaceID, item.StartDate, item.EndDate, candidateItem.StartDate, candidateItem.EndDate)
			}
		}
	}
}
func TestScoringProfilesSeparateSystemAndUserPreferences(t *testing.T) {
	s := testServer(t)
	systemReq := RecommendationRequest{TripDays: 2, Period: "day", ScoringProfile: "system", Preferences: validRequest().Preferences, Requirements: map[string]any{"placeType": "national_park"}}
	dates, flex, err := s.validateRequest(&systemReq)
	if err != nil || !flex || dates != nil {
		t.Fatalf("system profile validation dates=%v flex=%v err=%v", dates, flex, err)
	}
	if systemReq.Preferences == nil || systemReq.Preferences.Temperature.MinC != 25 || systemReq.Preferences.Temperature.MaxC != 30 || systemReq.Preferences.Rain.Preference != "dry" || systemReq.Preferences.Temperature.Weight != 1 || systemReq.Preferences.Rain.Weight != 1 || systemReq.Preferences.Air.Weight != 1 {
		t.Fatalf("system baseline was not applied: %+v", systemReq.Preferences)
	}
	response := s.makeResponse(systemReq, dates, flex)
	if response.Search["scoringProfile"] != "system" {
		t.Fatalf("response did not preserve system scoring profile: %+v", response.Search)
	}
	userReq := RecommendationRequest{TripDays: 2, Period: "day", ScoringProfile: "user", Requirements: map[string]any{"placeType": "national_park"}}
	if _, _, err := s.validateRequest(&userReq); err == nil || err.Error() != "preferences_required" {
		t.Fatalf("missing user preferences error=%v", err)
	}
	invalidReq := validRequest()
	invalidReq.ScoringProfile = "unknown"
	if _, _, err := s.validateRequest(&invalidReq); err == nil || err.Error() != "invalid_scoring_profile" {
		t.Fatalf("invalid scoring profile error=%v", err)
	}
}
func TestSeasonalOptionalDailyRequirementIsIncomplete(t *testing.T) {
	s := testServer(t)
	req := validRequest()
	req.Requirements["maxDailyUsAqiPm25"] = float64(50)
	resp := s.makeResponse(req, nil, true)
	if len(resp.Groups) != 1 || resp.Groups[0].Status != "incomplete" {
		t.Fatalf("seasonal daily requirement should make group incomplete: %+v", resp.Groups)
	}
	if resp.Groups[0].Items[0].Requirements[1].Status != "unknown" {
		t.Fatalf("expected unknown seasonal daily requirement: %+v", resp.Groups[0].Items[0].Requirements)
	}
}
func TestForecastGroupsAndHourWeightedScore(t *testing.T) {
	s := testServer(t)
	req := validRequest()
	explicitDates(&req, "2026-09-12", "2026-09-13")
	dates, flex, err := s.validateRequest(&req)
	if err != nil || flex {
		t.Fatalf("validation dates=%v flex=%v err=%v", dates, flex, err)
	}
	resp := s.makeResponse(req, dates, flex)
	if len(resp.Groups) != 3 {
		t.Fatalf("expected three groups, got %d", len(resp.Groups))
	}
	if len(resp.Groups[0].Items) != 24 || len(resp.Groups[1].Items) != 0 || len(resp.Groups[2].Items) != 0 {
		t.Fatalf("unexpected group counts: %d/%d/%d", len(resp.Groups[0].Items), len(resp.Groups[1].Items), len(resp.Groups[2].Items))
	}
	item := resp.Groups[0].Items[0]
	if item.Score == nil || item.Coverage.ObservedWeightedRatio < .99 {
		t.Fatalf("expected complete forecast score: %+v", item)
	}
	if item.Factors[0].AvailableHours != 48 || item.Factors[2].AvailableHours != 48 {
		t.Fatalf("expected 48 selected hours: %+v", item.Factors)
	}
	if item.Metrics == nil || item.Metrics.TemperatureC == nil || item.Metrics.RainMmPerHour == nil || item.Metrics.UsAQIPM25 == nil {
		t.Fatalf("forecast item missing card metrics: %+v", item.Metrics)
	}
}
func TestForecastIncompleteAndRequirementFailure(t *testing.T) {
	s := testServer(t)
	req := validRequest()
	explicitDates(&req, "2026-09-25", "2026-09-26")
	resp := s.makeResponse(req, mustDates(s, &req), false)
	if len(resp.Groups[1].Items) != 24 {
		t.Fatalf("missing air should produce 24 incomplete items, got %d", len(resp.Groups[1].Items))
	}
	req = validRequest()
	req.Requirements["maxTemperatureC"] = float64(0)
	explicitDates(&req, "2026-09-12", "2026-09-13")
	resp = s.makeResponse(req, mustDates(s, &req), false)
	if len(resp.Groups[2].Items) != 24 || resp.Groups[2].Items[0].Requirements[1].Status != "failed" {
		t.Fatalf("expected requirement failure: groups=%d req=%+v", len(resp.Groups[2].Items), resp.Groups[2].Items[0].Requirements)
	}
}
func TestDetailHasDailyHourlySourcesAndOutlook(t *testing.T) {
	s := testServer(t)
	req := validRequest()
	body := map[string]any{"dates": map[string]string{"startDate": "2026-09-12", "endDate": "2026-09-13"}, "tripDays": 2, "period": "all", "scoringProfile": "user", "preferences": req.Preferences, "requirements": req.Requirements, "placeId": "park-01", "selectedStartDate": "2026-09-12", "selectedEndDate": "2026-09-13", "mode": "forecast"}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/recommendations/detail", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	s.recommendationDetail(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
	}
	var resp RecommendationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var item *Item
	for i := range resp.Groups[0].Items {
		if resp.Groups[0].Items[i].PlaceID == "park-01" {
			item = &resp.Groups[0].Items[i]
			break
		}
	}
	if item == nil || item.Details == nil || len(item.Details.Hourly) != 48 || len(item.Details.Daily) != 2 || len(item.Details.Sources) == 0 {
		t.Fatalf("detail incomplete: %+v", item)
	}
	if item.StartDate != "2026-09-12" || item.EndDate != "2026-09-13" {
		t.Fatalf("range changed: %s..%s", item.StartDate, item.EndDate)
	}
	if item.Details.Daily[0].AirScore == nil {
		t.Fatal("daily detail must expose the AQI-derived score")
	}
}
func TestSeasonalDetailAcceptsCardWindowContext(t *testing.T) {
	s := testServer(t)
	req := validRequest()
	body := map[string]any{"dates": nil, "tripDays": 2, "period": "day", "scoringProfile": "user", "preferences": req.Preferences, "requirements": req.Requirements, "placeId": "park-01", "selectedStartDate": "2026-09-12", "selectedEndDate": "2026-09-13", "mode": "seasonal"}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/recommendations/detail", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	s.recommendationDetail(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("seasonal detail status=%d body=%s", w.Code, w.Body.String())
	}
	var resp RecommendationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var item *Item
	for i := range resp.Groups[0].Items {
		if resp.Groups[0].Items[i].PlaceID == "park-01" {
			item = &resp.Groups[0].Items[i]
			break
		}
	}
	if item == nil || item.Details == nil || len(item.Details.SeasonalYears) == 0 {
		t.Fatalf("seasonal detail missing years: %+v", item)
	}
	if resp.Search["mode"] != "seasonal" {
		t.Fatalf("expected seasonal response mode, got %v", resp.Search["mode"])
	}
}
func TestSEAS5OutlookUsesPlaceCoordinates(t *testing.T) {
	s := testServer(t)
	a := s.outlook("park-01", "2026-09-12")
	b := s.outlook("park-03", "2026-09-12")
	if a == nil || b == nil || a.AnomalyK == b.AnomalyK {
		t.Fatalf("expected place-specific SEAS5 outlook, got %+v and %+v", a, b)
	}
}
func TestUpstreamTimeoutFailsClosed(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := fetchURL(ctx, client, "https://provider.invalid/slow"); err == nil {
		t.Fatal("timed out upstream unexpectedly returned success")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func TestAQIAdapterBoundaries(t *testing.T) {
	cases := []struct {
		value float64
		want  int
	}{{9.099, 50}, {9.1, 51}, {35.499, 100}, {35.5, 101}, {125.4, 200}, {125.5, 201}, {325.4, 500}, {425.3, 699}}
	for _, c := range cases {
		got := aqiPM25(c.value)
		if got == nil || *got != c.want {
			t.Errorf("AQI(%v)=%v want %d", c.value, got, c.want)
		}
	}
}
func TestValidationBoundaryAndNight(t *testing.T) {
	s := testServer(t)
	req := validRequest()
	req.Period = "night"
	explicitDates(&req, "2026-09-12", "2026-09-12")
	if _, _, err := s.validateRequest(&req); err == nil || err.Error() != "single_day_night_not_supported" {
		t.Fatalf("one-day night error=%v", err)
	}
	req = validRequest()
	explicitDates(&req, "2026-09-12", "2026-10-12")
	if _, _, err := s.validateRequest(&req); err == nil || err.Error() != "date_out_of_30_day_horizon" {
		t.Fatalf("30-day horizon error=%v", err)
	}
}
func TestCacheAndCORS(t *testing.T) {
	s := testServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/recommendations", s.recommendations)
	raw, _ := json.Marshal(validRequest())
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodPost, "/v1/recommendations", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		withCORS(mux).ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") == "" {
			t.Fatalf("request %d status=%d", i, w.Code)
		}
		if i == 1 && !bytes.Contains(w.Body.Bytes(), []byte("served_from_cache")) {
			t.Fatalf("cache not used: %s", w.Body.String())
		}
	}
}

func TestGinRouterAndCORS(t *testing.T) {
	s := testServer(t)
	router := newRouter(s)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("gin health route status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
	preflight := httptest.NewRequest(http.MethodOptions, "/v1/recommendations", nil)
	preflight.Header.Set("Origin", "http://127.0.0.1:13001")
	preflightResponse := httptest.NewRecorder()
	router.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent || preflightResponse.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:13001" {
		t.Fatalf("gin preflight status=%d headers=%v", preflightResponse.Code, preflightResponse.Header())
	}
}

func TestVercelHandlerHealthz(t *testing.T) {
	t.Setenv("SUPABASE_URL", "")
	t.Setenv("SUPABASE_SERVICE_ROLE_KEY", "")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	Handler(resp, req)
	if resp.Code != http.StatusOK || !bytes.Contains(resp.Body.Bytes(), []byte(`"status":"ok"`)) {
		t.Fatalf("vercel handler health status=%d body=%s", resp.Code, resp.Body.String())
	}
	if os.Getenv("SUPABASE_SERVICE_ROLE_KEY") != "" {
		t.Fatal("test environment unexpectedly retained a Supabase service key")
	}
}

func TestHTTPValidationErrors(t *testing.T) {
	s := testServer(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/recommendations", s.recommendations)
	bad := httptest.NewRequest(http.MethodPost, "/v1/recommendations", bytes.NewBufferString("{"))
	badResponse := httptest.NewRecorder()
	mux.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusBadRequest || !bytes.Contains(badResponse.Body.Bytes(), []byte("malformed_json")) {
		t.Fatalf("malformed request status=%d body=%s", badResponse.Code, badResponse.Body.String())
	}
	missingProfile := httptest.NewRequest(http.MethodPost, "/v1/recommendations", bytes.NewBufferString(`{"tripDays":2,"period":"all","requirements":{"placeType":"national_park"}}`))
	missingProfileResponse := httptest.NewRecorder()
	mux.ServeHTTP(missingProfileResponse, missingProfile)
	if missingProfileResponse.Code != http.StatusUnprocessableEntity || !bytes.Contains(missingProfileResponse.Body.Bytes(), []byte("scoring_profile_required")) {
		t.Fatalf("missing scoring profile status=%d body=%s", missingProfileResponse.Code, missingProfileResponse.Body.String())
	}
	missing := httptest.NewRequest(http.MethodPost, "/v1/recommendations", bytes.NewBufferString(`{"tripDays":2,"period":"all","scoringProfile":"user","requirements":{"placeType":"national_park"}}`))
	missingResponse := httptest.NewRecorder()
	mux.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusUnprocessableEntity || !bytes.Contains(missingResponse.Body.Bytes(), []byte("preferences_required")) {
		t.Fatalf("missing prefs status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
}
func TestExpiredCacheEntryIsIgnored(t *testing.T) {
	s := testServer(t)
	s.cache["expired"] = cacheEntry{Response: RecommendationResponse{RequestID: "old"}, ExpiresAt: s.clock().Add(-time.Second)}
	if _, ok := s.cacheGet("expired"); ok {
		t.Fatal("expired cache entry was returned")
	}
	if _, ok := s.cache["expired"]; ok {
		t.Fatal("expired cache entry was not evicted")
	}
}
func mustDates(s *server, req *RecommendationRequest) []time.Time {
	d, _, err := s.validateRequest(req)
	if err != nil {
		panic(err)
	}
	return d
}
