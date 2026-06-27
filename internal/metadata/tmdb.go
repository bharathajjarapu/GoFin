package metadata

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gofin/internal/config"
)

type Client struct {
	Enabled bool
	Key     string
	Lang    string
	HTTP    *http.Client
}

type Person struct {
	Name, Role, Type string
}

type ExternalURL struct {
	Name, URL string
}

type Result struct {
	Name, Overview, PremiereDate, PosterURL, BackdropURL string
	Genres, Studios, Taglines                            []string
	People                                               []Person
	ExternalURLs                                         []ExternalURL
	OfficialRating                                       string
	CommunityRating                                      float64
	RuntimeTicks                                         int64
	Year, Season, Episode, TMDBID                        int
}

func New(c config.Metadata) Client {
	return Client{Enabled: c.Enabled && c.Provider == "tmdb", Key: os.Getenv(c.APIKeyEnv), Lang: c.Language, HTTP: &http.Client{Timeout: 8 * time.Second}}
}

func (c Client) Movie(name string, year int) (Result, bool) {
	r, ok := c.search("movie", name, year)
	if !ok || r.TMDBID == 0 {
		return r, ok
	}
	if d, ok := c.movieDetails(r.TMDBID); ok {
		return d, true
	}
	return r, true
}
func (c Client) Series(name string, year int) (Result, bool) {
	r, ok := c.search("tv", name, year)
	if !ok || r.TMDBID == 0 {
		return r, ok
	}
	if d, ok := c.seriesDetails(r.TMDBID); ok {
		return d, true
	}
	return r, true
}
func (c Client) Season(seriesID, season int) (Result, bool) {
	var b seasonBody
	if !c.get("tv/"+strconv.Itoa(seriesID)+"/season/"+strconv.Itoa(season), "", &b) {
		return Result{}, false
	}
	r := Result{Name: b.Name, Overview: b.Overview, PremiereDate: b.AirDate, Season: season, TMDBID: b.ID, Year: yearFromDate(b.AirDate)}
	if b.PosterPath != "" {
		r.PosterURL = imageURL(b.PosterPath)
	}
	return r, true
}
func (c Client) Episode(seriesID, season, episode int) (Result, bool) {
	var b episodeBody
	path := "tv/" + strconv.Itoa(seriesID) + "/season/" + strconv.Itoa(season) + "/episode/" + strconv.Itoa(episode)
	if !c.get(path, "credits,external_ids,images", &b) {
		return Result{}, false
	}
	r := Result{Name: b.Name, Overview: b.Overview, PremiereDate: b.AirDate, TMDBID: b.ID, Season: season, Episode: episode, Year: yearFromDate(b.AirDate), RuntimeTicks: int64(b.Runtime) * 60 * 10000000}
	if b.StillPath != "" {
		r.PosterURL = imageURL(b.StillPath)
	}
	for i, p := range b.Credits.Cast {
		if i == 10 {
			break
		}
		r.People = append(r.People, Person{Name: p.Name, Role: p.Character, Type: "Actor"})
	}
	for _, p := range b.Credits.Crew {
		if p.Job == "Director" || p.Job == "Writer" || p.Job == "Screenplay" {
			r.People = append(r.People, Person{Name: p.Name, Role: p.Job, Type: p.Job})
		}
	}
	return r, true
}

func (c Client) search(kind, name string, year int) (Result, bool) {
	if !c.Enabled || c.Key == "" || name == "" {
		return Result{}, false
	}
	v := url.Values{"api_key": {c.Key}, "query": {name}, "language": {c.Lang}}
	if year > 0 {
		if kind == "movie" {
			v.Set("year", strconv.Itoa(year))
		} else {
			v.Set("first_air_date_year", strconv.Itoa(year))
		}
	}
	u := "https://api.themoviedb.org/3/search/" + kind + "?" + v.Encode()
	res, err := c.HTTP.Get(u)
	if err != nil {
		return Result{}, false
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return Result{}, false
	}
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if json.NewDecoder(res.Body).Decode(&body) != nil || len(body.Results) == 0 {
		return Result{}, false
	}
	m := body.Results[0]
	r := Result{Overview: str(m["overview"]), TMDBID: num(m["id"])}
	if kind == "movie" {
		r.Name, r.PremiereDate = str(m["title"]), str(m["release_date"])
	} else {
		r.Name, r.PremiereDate = str(m["name"]), str(m["first_air_date"])
	}
	r.Year = yearFromDate(r.PremiereDate)
	if p := str(m["poster_path"]); p != "" {
		r.PosterURL = imageURL(p)
	}
	if p := str(m["backdrop_path"]); p != "" {
		r.BackdropURL = imageURL(p)
	}
	return r, true
}

func (c Client) movieDetails(id int) (Result, bool) {
	var b movieBody
	if !c.get("movie/"+strconv.Itoa(id), "credits,external_ids,release_dates", &b) {
		return Result{}, false
	}
	r := Result{Name: b.Title, Overview: b.Overview, PremiereDate: b.ReleaseDate, TMDBID: b.ID, CommunityRating: b.VoteAverage, OfficialRating: b.certification()}
	r.Year = yearFromDate(r.PremiereDate)
	r.RuntimeTicks = int64(b.Runtime) * 60 * 10000000
	if b.Tagline != "" {
		r.Taglines = []string{b.Tagline}
	}
	if b.PosterPath != "" {
		r.PosterURL = imageURL(b.PosterPath)
	}
	if b.BackdropPath != "" {
		r.BackdropURL = imageURL(b.BackdropPath)
	}
	for _, g := range b.Genres {
		r.Genres = append(r.Genres, g.Name)
	}
	for _, s := range b.Studios {
		r.Studios = append(r.Studios, s.Name)
	}
	for i, p := range b.Credits.Cast {
		if i == 10 {
			break
		}
		r.People = append(r.People, Person{Name: p.Name, Role: p.Character, Type: "Actor"})
	}
	for _, p := range b.Credits.Crew {
		if p.Job == "Director" || p.Job == "Writer" || p.Job == "Screenplay" {
			r.People = append(r.People, Person{Name: p.Name, Role: p.Job, Type: p.Job})
		}
	}
	r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "TheMovieDb", URL: "https://www.themoviedb.org/movie/" + strconv.Itoa(id)})
	if b.ExternalIDs.IMDBID != "" {
		r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "IMDb", URL: "https://www.imdb.com/title/" + b.ExternalIDs.IMDBID + "/"})
	}
	return r, true
}

func (c Client) seriesDetails(id int) (Result, bool) {
	var b seriesBody
	if !c.get("tv/"+strconv.Itoa(id), "aggregate_credits,external_ids,content_ratings", &b) {
		return Result{}, false
	}
	r := Result{Name: b.Name, Overview: b.Overview, PremiereDate: b.FirstAirDate, TMDBID: b.ID, CommunityRating: b.VoteAverage, OfficialRating: b.contentRating()}
	r.Year = yearFromDate(r.PremiereDate)
	if len(b.Runtimes) > 0 {
		r.RuntimeTicks = int64(b.Runtimes[0]) * 60 * 10000000
	}
	if b.PosterPath != "" {
		r.PosterURL = imageURL(b.PosterPath)
	}
	if b.BackdropPath != "" {
		r.BackdropURL = imageURL(b.BackdropPath)
	}
	for _, g := range b.Genres {
		r.Genres = append(r.Genres, g.Name)
	}
	for _, n := range b.Networks {
		r.Studios = append(r.Studios, n.Name)
	}
	for i, p := range b.Credits.Cast {
		if i == 10 {
			break
		}
		role := ""
		if len(p.Roles) > 0 {
			role = p.Roles[0].Character
		}
		r.People = append(r.People, Person{Name: p.Name, Role: role, Type: "Actor"})
	}
	for _, p := range b.Creators {
		r.People = append(r.People, Person{Name: p.Name, Role: "Creator", Type: "Creator"})
	}
	r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "TheMovieDb", URL: "https://www.themoviedb.org/tv/" + strconv.Itoa(id)})
	if b.ExternalIDs.IMDBID != "" {
		r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "IMDb", URL: "https://www.imdb.com/title/" + b.ExternalIDs.IMDBID + "/"})
	}
	return r, true
}

func (c Client) get(path, appendTo string, v any) bool {
	if !c.Enabled || c.Key == "" {
		return false
	}
	q := url.Values{"api_key": {c.Key}, "language": {c.Lang}}
	if appendTo != "" {
		q.Set("append_to_response", appendTo)
	}
	res, err := c.HTTP.Get("https://api.themoviedb.org/3/" + path + "?" + q.Encode())
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode/100 == 2 && json.NewDecoder(res.Body).Decode(v) == nil
}

func ProviderIDs(tmdb int) string {
	if tmdb == 0 {
		return ""
	}
	return fmt.Sprintf(`{"Tmdb":"%d"}`, tmdb)
}

type named struct{ Name string }

type movieBody struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	Tagline      string  `json:"tagline"`
	Runtime      int     `json:"runtime"`
	VoteAverage  float64 `json:"vote_average"`
	Genres       []named `json:"genres"`
	Studios      []named `json:"production_companies"`
	Credits      struct {
		Cast []struct{ Name, Character string } `json:"cast"`
		Crew []struct{ Name, Job string }       `json:"crew"`
	} `json:"credits"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
}

type seriesBody struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	FirstAirDate string  `json:"first_air_date"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	VoteAverage  float64 `json:"vote_average"`
	Runtimes     []int   `json:"episode_run_time"`
	Genres       []named `json:"genres"`
	Networks     []named `json:"networks"`
	Creators     []named `json:"created_by"`
	Credits      struct {
		Cast []struct {
			Name  string
			Roles []struct{ Character string } `json:"roles"`
		} `json:"cast"`
	} `json:"aggregate_credits"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
	Ratings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
}

type seasonBody struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Overview   string `json:"overview"`
	AirDate    string `json:"air_date"`
	PosterPath string `json:"poster_path"`
}

type episodeBody struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Overview  string `json:"overview"`
	AirDate   string `json:"air_date"`
	StillPath string `json:"still_path"`
	Runtime   int    `json:"runtime"`
	Credits   struct {
		Cast []struct{ Name, Character string } `json:"cast"`
		Crew []struct{ Name, Job string }       `json:"crew"`
	} `json:"credits"`
}

func (b movieBody) certification() string {
	for _, r := range b.ReleaseDates.Results {
		if r.Country != "US" {
			continue
		}
		for _, d := range r.Dates {
			if d.Certification != "" {
				return d.Certification
			}
		}
	}
	return ""
}

func (b seriesBody) contentRating() string {
	for _, r := range b.Ratings.Results {
		if r.Country == "US" && r.Rating != "" {
			return r.Rating
		}
	}
	return ""
}

func imageURL(p string) string { return "https://image.tmdb.org/t/p/original" + p }
func str(v any) string         { s, _ := v.(string); return s }
func num(v any) int            { f, _ := v.(float64); return int(f) }
func yearFromDate(s string) int {
	if len(s) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(s[:4])
	return y
}
func CleanYear(name string) (string, int) {
	parts := strings.Fields(name)
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.Trim(parts[i], "()[]")
		if len(p) == 4 {
			y, _ := strconv.Atoi(p)
			if y > 1880 && y < 2200 {
				return strings.Join(append(parts[:i], parts[i+1:]...), " "), y
			}
		}
	}
	return name, 0
}
