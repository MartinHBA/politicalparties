package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
)

type Party struct {
	Name  string
	Seats int
	Color string
}

func main() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/results", resultsHandler)
	http.HandleFunc("/submit", submitHandler)

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

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	partiesJSON := r.FormValue("parties")
	var parties []Party
	err = json.Unmarshal([]byte(partiesJSON), &parties)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	combinations := findCombinations(parties, 76)

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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(chartData)
}

func findCombinations(parties []Party, target int) [][]Party {
	var result [][]Party
	findCombinationsRec(parties, target, 0, []Party{}, &result)
	return result
}

func findCombinationsRec(parties []Party, target, currentSum int, currentCombination []Party, result *[][]Party) {
	if currentSum >= target {
		combinationCopy := make([]Party, len(currentCombination))
		copy(combinationCopy, currentCombination)
		*result = append(*result, combinationCopy)
		return
	}

	for i, party := range parties {
		remaining := append([]Party{}, parties[i+1:]...)
		newCombination := append(currentCombination, party)
		findCombinationsRec(remaining, target, currentSum+party.Seats, newCombination, result)
	}
}
