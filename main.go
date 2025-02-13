package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"
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

var partyColors map[string]string

// develop branch comment
func main() {

	var err error
	partyColors, err = loadPartyColors("PartyColors.csv")
	if err != nil {
		log.Fatal("Error loading party colors: ", err)
	}

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/percent", percentHandler)
	http.HandleFunc("/results", resultsHandler)
	http.HandleFunc("/submit", submitHandler)
	http.HandleFunc("/exclusions", exclusionsHandler)
	http.HandleFunc("/submit_with_exclusions", submitWithExclusionsHandler)
	http.HandleFunc("/fetch", fetchHandler)
	log.Println(pairs)
	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))

}

func loadPartyColors(filename string) (map[string]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ','
	reader.LazyQuotes = true

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	colors := make(map[string]string)
	// Assuming the first row is a header
	for i, row := range rows {
		if i == 0 {
			continue
		}
		if len(row) >= 2 {
			colors[row[0]] = row[1]
		}
	}
	return colors, nil
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
	agencies, err := fetchAllAgencies("PollsSeats.csv")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tmpl, err := template.ParseFiles("index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = tmpl.Execute(w, agencies)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func fetchAllAgencies(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ','
	reader.LazyQuotes = true

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	type AgencyDate struct {
		Name string
		Date time.Time
	}

	agencyMap := make(map[string]time.Time)
	// Start the loop from index 1 to skip the first row
	for i, row := range rows {
		if i == 0 {
			continue // Skip the first row
		}
		date, err := time.Parse("02.01.2006", row[0])
		if err != nil {
			return nil, err
		}
		agencyWithDate := row[1] + " - " + row[0]
		agencyMap[agencyWithDate] = date
	}

	// Convert the map to a slice
	var agencies []AgencyDate
	for name, date := range agencyMap {
		agencies = append(agencies, AgencyDate{Name: name, Date: date})
	}

	// Sort the agencies by date in descending order
	sort.Slice(agencies, func(i, j int) bool {
		return agencies[i].Date.After(agencies[j].Date)
	})

	// Extract the agency names
	var sortedAgencies []string
	for _, agency := range agencies {
		sortedAgencies = append(sortedAgencies, agency.Name)
	}

	return sortedAgencies, nil
}

func contains(slice []string, item string) bool {
	for _, a := range slice {
		if a == item {
			return true
		}
	}
	return false
}

func percentHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("percent.html")
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

func fetchHandler(w http.ResponseWriter, r *http.Request) {
	filename := "PollsSeats.csv"
	agency := r.URL.Query().Get("source")

	parties, date, err := fetchAndFilterParties(filename, agency)
	if err != nil {
		http.Error(w, "Error fetching data: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err != nil {
		log.Println("Error fetching data:", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Error fetching data"})
		return
	}

	if len(parties) == 0 {
		log.Println("No parties found.")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"error": "No parties found."})
		return
	}

	for _, party := range parties {
		log.Printf("Party: %s, Seats: %d, Color: %s", party.Name, party.Seats, party.Color)
	}
	fmt.Println(date)

	result := map[string]interface{}{
		"parties": parties,
		"date":    date,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

func fetchAndFilterParties(filename string, agency string) ([]Party, string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ','
	reader.LazyQuotes = true

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, "", err
	}

	var parties []Party
	var date string
	for _, row := range rows {
		// Check that the row has at least 4 columns (date, agency, name, seat)
		if len(row) < 4 {
			continue
		}
		if row[1] == agency {
			if date == "" {
				date = row[0]
			}
			seats, err := strconv.Atoi(row[3])
			if err != nil {
				return nil, "", err
			}

			// Look up the color from the partyColors mapping.
			color, ok := partyColors[row[2]]
			if !ok {
				// If no color is found, you can assign a default color.
				color = "#000000"
			}

			party := Party{
				Name:  row[2],
				Seats: seats,
				Color: color,
			}
			parties = append(parties, party)
		}
	}

	return parties, date, nil
}
