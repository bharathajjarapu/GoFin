package metadata

import (
	"encoding/json"
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
	Name, Role, Type, ProfileURL, IMDBID                           string
	Biography, BirthDate, DeathDate, PlaceOfBirth, KnownDepartment string
	TMDBID                                                         int
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
	IMDBID                                               string
}

func New(c config.Metadata) Client {
	return Client{Enabled: c.Enabled, Key: os.Getenv(c.APIKeyEnv), Lang: c.Language, HTTP: &http.Client{Timeout: 8 * time.Second}}
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
func (c Client) MovieByID(id int) (Result, bool)  { return c.movieDetails(id) }
func (c Client) SeriesByID(id int) (Result, bool) { return c.seriesDetails(id) }
func (c Client) MovieByIMDB(id string) (Result, bool) {
	tmdb, kind, ok := c.findByExternalID(id, "imdb_id")
	if !ok || kind != "movie" {
		return Result{}, false
	}
	return c.movieDetails(tmdb)
}
func (c Client) SeriesByIMDB(id string) (Result, bool) {
	tmdb, kind, ok := c.findByExternalID(id, "imdb_id")
	if !ok || kind != "tv" {
		return Result{}, false
	}
	return c.seriesDetails(tmdb)
}
func (c Client) SeriesByTVDB(id string) (Result, bool) {
	tmdb, kind, ok := c.findByExternalID(id, "tvdb_id")
	if !ok || kind != "tv" {
		return Result{}, false
	}
	return c.seriesDetails(tmdb)
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
func (c Client) PersonDetails(id int) (Person, bool) {
	var b personBody
	if !c.get("person/"+strconv.Itoa(id), "external_ids", &b) {
		return Person{}, false
	}
	p := Person{Name: b.Name, TMDBID: b.ID, Biography: b.Biography, BirthDate: b.Birthday, DeathDate: b.Deathday, PlaceOfBirth: b.PlaceOfBirth, KnownDepartment: b.KnownForDepartment}
	if b.ProfilePath != "" {
		p.ProfileURL = imageURL(b.ProfilePath)
	}
	p.IMDBID = b.ExternalIDs.IMDBID
	return p, true
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
	r.IMDBID = b.ExternalIDs.IMDBID
	for i, p := range b.Credits.Cast {
		if i == 15 {
			break
		}
		r.People = append(r.People, person(p.ID, p.Name, p.Character, "Actor", p.ProfilePath))
	}
	for _, p := range b.Credits.Crew {
		if p.Job == "Director" || p.Job == "Writer" || p.Job == "Screenplay" {
			r.People = append(r.People, person(p.ID, p.Name, p.Job, p.Job, p.ProfilePath))
		}
	}
	if r.IMDBID != "" {
		r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "IMDb", URL: "https://www.imdb.com/title/" + r.IMDBID + "/"})
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

func (c Client) findByExternalID(id, source string) (int, string, bool) {
	var b struct {
		Movie []struct {
			ID int `json:"id"`
		} `json:"movie_results"`
		TV []struct {
			ID int `json:"id"`
		} `json:"tv_results"`
	}
	if !c.getValues("find/"+url.PathEscape(id), url.Values{"external_source": {source}}, &b) {
		return 0, "", false
	}
	if len(b.Movie) > 0 {
		return b.Movie[0].ID, "movie", true
	}
	if len(b.TV) > 0 {
		return b.TV[0].ID, "tv", true
	}
	return 0, "", false
}

func (c Client) movieDetails(id int) (Result, bool) {
	var b movieBody
	if !c.get("movie/"+strconv.Itoa(id), "credits,external_ids,release_dates", &b) {
		return Result{}, false
	}
	r := Result{Name: b.Title, Overview: b.Overview, PremiereDate: b.ReleaseDate, TMDBID: b.ID, CommunityRating: b.VoteAverage, OfficialRating: b.certification()}
	r.IMDBID = b.ExternalIDs.IMDBID
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
		if i == 15 {
			break
		}
		r.People = append(r.People, person(p.ID, p.Name, p.Character, "Actor", p.ProfilePath))
	}
	for _, p := range b.Credits.Crew {
		if p.Job == "Director" || p.Job == "Writer" || p.Job == "Screenplay" {
			r.People = append(r.People, person(p.ID, p.Name, p.Job, p.Job, p.ProfilePath))
		}
	}
	r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "TheMovieDb", URL: "https://www.themoviedb.org/movie/" + strconv.Itoa(id)})
	if r.IMDBID != "" {
		r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "IMDb", URL: "https://www.imdb.com/title/" + r.IMDBID + "/"})
	}
	return r, true
}

func (c Client) seriesDetails(id int) (Result, bool) {
	var b seriesBody
	if !c.get("tv/"+strconv.Itoa(id), "aggregate_credits,external_ids,content_ratings", &b) {
		return Result{}, false
	}
	r := Result{Name: b.Name, Overview: b.Overview, PremiereDate: b.FirstAirDate, TMDBID: b.ID, CommunityRating: b.VoteAverage, OfficialRating: b.contentRating()}
	r.IMDBID = b.ExternalIDs.IMDBID
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
		if i == 15 {
			break
		}
		role := ""
		if len(p.Roles) > 0 {
			role = p.Roles[0].Character
		}
		r.People = append(r.People, person(p.ID, p.Name, role, "Actor", p.ProfilePath))
	}
	for _, p := range b.Creators {
		r.People = append(r.People, person(p.ID, p.Name, "Creator", "Creator", p.ProfilePath))
	}
	r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "TheMovieDb", URL: "https://www.themoviedb.org/tv/" + strconv.Itoa(id)})
	if r.IMDBID != "" {
		r.ExternalURLs = append(r.ExternalURLs, ExternalURL{Name: "IMDb", URL: "https://www.imdb.com/title/" + r.IMDBID + "/"})
	}
	return r, true
}

func (c Client) get(path, appendTo string, v any) bool {
	q := url.Values{}
	if appendTo != "" {
		q.Set("append_to_response", appendTo)
	}
	return c.getValues(path, q, v)
}

func (c Client) getValues(path string, values url.Values, v any) bool {
	if !c.Enabled || c.Key == "" {
		return false
	}
	values.Set("api_key", c.Key)
	values.Set("language", c.Lang)
	res, err := c.HTTP.Get("https://api.themoviedb.org/3/" + path + "?" + values.Encode())
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode/100 == 2 && json.NewDecoder(res.Body).Decode(v) == nil
}

func ProviderIDs(tmdb int, imdb string) string {
	if tmdb == 0 && imdb == "" {
		return ""
	}
	ids := map[string]string{}
	if tmdb != 0 {
		ids["Tmdb"] = strconv.Itoa(tmdb)
	}
	if imdb != "" {
		ids["Imdb"] = imdb
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

type named struct{ Name string }
type personRef struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ProfilePath string `json:"profile_path"`
}

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
		Cast []credit `json:"cast"`
		Crew []credit `json:"crew"`
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
	ID           int         `json:"id"`
	Name         string      `json:"name"`
	Overview     string      `json:"overview"`
	FirstAirDate string      `json:"first_air_date"`
	PosterPath   string      `json:"poster_path"`
	BackdropPath string      `json:"backdrop_path"`
	VoteAverage  float64     `json:"vote_average"`
	Runtimes     []int       `json:"episode_run_time"`
	Genres       []named     `json:"genres"`
	Networks     []named     `json:"networks"`
	Creators     []personRef `json:"created_by"`
	Credits      struct {
		Cast []struct {
			ID          int                          `json:"id"`
			Name        string                       `json:"name"`
			ProfilePath string                       `json:"profile_path"`
			Roles       []struct{ Character string } `json:"roles"`
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
		Cast []credit `json:"cast"`
		Crew []credit `json:"crew"`
	} `json:"credits"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
}

type credit struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	Job         string `json:"job"`
	ProfilePath string `json:"profile_path"`
}

type personBody struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Biography          string `json:"biography"`
	Birthday           string `json:"birthday"`
	Deathday           string `json:"deathday"`
	PlaceOfBirth       string `json:"place_of_birth"`
	KnownForDepartment string `json:"known_for_department"`
	ProfilePath        string `json:"profile_path"`
	ExternalIDs        struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
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
func person(id int, name, role, typ, profile string) Person {
	p := Person{Name: name, Role: role, Type: typ, TMDBID: id}
	if profile != "" {
		p.ProfileURL = imageURL(profile)
	}
	return p
}
func str(v any) string { s, _ := v.(string); return s }
func num(v any) int    { f, _ := v.(float64); return int(f) }
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
