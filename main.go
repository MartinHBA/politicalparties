package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gocolly/colly/v2"
)

type Party struct {
	Name  string
	Seats int
	Color string
}

type ExclusionPair struct {
	FirstParty  string
	SecondParty string
}

var pairs = []ExclusionPair{}

// develop branch comment
func main() {

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/results", resultsHandler)
	http.HandleFunc("/submit", submitHandler)
	http.HandleFunc("/exclusions", exclusionsHandler)
	http.HandleFunc("/submit_with_exclusions", submitWithExclusionsHandler)
	http.HandleFunc("/fetch", fetchHandler)
	log.Println(pairs)
	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))

}

func resultsHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("results.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = tmpl.Execute(w, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = tmpl.Execute(w, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func processRequest(r *http.Request, pairs []ExclusionPair) ([]map[string]interface{}, error) {
	err := r.ParseForm()
	if err != nil {
		return nil, err
	}

	partiesJSON := r.FormValue("parties")
	var parties []Party
	err = json.Unmarshal([]byte(partiesJSON), &parties)
	if err != nil {
		return nil, err
	}

	combinations := findCombinations(parties, 76, pairs)

	var chartData []map[string]interface{}
	for _, combination := range combinations {
		labels := make([]string, len(combination))
		values := make([]int, len(combination))
		colors := make([]string, len(combination))
		for j, party := range combination {
			labels[j] = party.Name
			values[j] = party.Seats
			colors[j] = party.Color
		}
		chartData = append(chartData, map[string]interface{}{
			"labels": labels,
			"values": values,
			"colors": colors,
		})
	}

	return chartData, nil
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var pairs []ExclusionPair
	chartData, err := processRequest(r, pairs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(chartData)
}

func submitWithExclusionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	exclusionJSON := r.FormValue("exclusions")
	var exclusionPairs []ExclusionPair
	err = json.Unmarshal([]byte(exclusionJSON), &exclusionPairs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fmt.Println("Exclusions: ", exclusionPairs)

	chartData, err := processRequest(r, exclusionPairs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(chartData)
}

func exclusionsHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Serving exclusions.html")

	tmpl, err := template.ParseFiles("exclusions.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = tmpl.Execute(w, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

}

func containsExclusionPairs(combination []Party, exclusionPairs []ExclusionPair) bool {
	partyNames := make(map[string]bool)
	for _, party := range combination {
		partyNames[party.Name] = true
	}

	for _, pair := range exclusionPairs {
		if partyNames[pair.FirstParty] && partyNames[pair.SecondParty] {
			return true
		}
	}

	return false
}

func findCombinations(parties []Party, target int, exclusionPairs []ExclusionPair) [][]Party {
	var result [][]Party
	findCombinationsRec(parties, target, 0, []Party{}, &result, exclusionPairs)
	return result
}

func findCombinationsRec(parties []Party, target, currentSum int, currentCombination []Party, result *[][]Party, exclusionPairs []ExclusionPair) {
	if currentSum >= target {
		if !containsExclusionPairs(currentCombination, exclusionPairs) {
			combinationCopy := make([]Party, len(currentCombination))
			copy(combinationCopy, currentCombination)
			*result = append(*result, combinationCopy)
		}
		return
	}

	for i, party := range parties {
		remaining := append([]Party{}, parties[i+1:]...)
		newCombination := append(currentCombination, party)
		findCombinationsRec(remaining, target, currentSum+party.Seats, newCombination, result, exclusionPairs)
	}
}

func extractColorFromStyle(style string) string {
	re := regexp.MustCompile(`background:\s*([^;]+)`)
	matches := re.FindStringSubmatch(style)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func scrape(source string) []Party {

	myurl := fmt.Sprintf("https://volby.sme.sk/pref/1/politicke-strany/p/%s", source)
	c := colly.NewCollector()

	var parties []Party

	c.OnHTML("div[class='my-l mb-xxl align-center'] a", func(el *colly.HTMLElement) {
		partyName := el.ChildText("div.h-tiny-bold")
		seatsStr := el.ChildText("div.bullet")
		colorStyle := el.ChildAttr("div.bullet", "style")
		color := extractColorFromStyle(colorStyle)

		seats, err := strconv.Atoi(strings.TrimSpace(seatsStr))
		if err != nil {
			log.Printf("Error converting seats to int: %v\n", err)
			return
		}

		if partyName != "" { //&& color != "" {
			party := Party{
				Name:  strings.TrimSpace(partyName),
				Seats: seats,
				Color: color,
			}
			parties = append(parties, party)
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		log.Println("Request URL:", r.Request.URL, "failed with response:", r, "\nError:", err)
	})

	c.OnRequest(func(r *colly.Request) {
		log.Println("Visiting", r.URL.String())
	})

	err := c.Visit(myurl)
	if err != nil {
		log.Println("Error visiting URL:", err)
	}

	if len(parties) == 0 {
		log.Println("No parties found. Please check the HTML structure and CSS selectors.")
	} else {
		for _, party := range parties {
			fmt.Printf("Party: %s, Seats: %d, Color: %s\n", party.Name, party.Seats, party.Color)
		}
	}

	return parties
}

func fetchHandler(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	parties := scrape(source)

	if len(parties) == 0 {
		log.Println("No parties found. Please check the HTML structure and CSS selectors.")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"error": "No parties found. Please check the HTML structure and CSS selectors."})
		return
	}

	for _, party := range parties {
		log.Printf("Party: %s, Seats: %d, Color: %s", party.Name, party.Seats, party.Color)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(parties)
}