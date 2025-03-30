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
	"strings" // Ensure strings package is imported
	"time"
)

type Party struct {
	Name  string `json:"name"`
	Seats int    `json:"seats"`
	Color string `json:"color"`
}

type ExclusionPair struct {
	FirstParty  string `json:"firstParty"`
	SecondParty string `json:"secondParty"`
}

var pairs = []ExclusionPair{}

var partyColors map[string]string
var creditInfoMap map[string]string

func main() {
	// Add log for current working directory
	wd, _ := os.Getwd()
	log.Println("Current Working Directory:", wd)

	var err error
	partyColors, err = loadPartyColors("PartyColors.csv")
	if err != nil {
		log.Fatalf("FATAL: Error loading party colors: %v", err) // More prominent log
	}

	creditInfoMap, err = loadCreditInfo("creditInfo.csv")
	if err != nil {
		log.Fatalf("FATAL: Error loading credit info: %v", err) // More prominent log
	}

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/results", resultsHandler)
	http.HandleFunc("/submit", submitHandler)
	http.HandleFunc("/exclusions", exclusionsHandler)
	http.HandleFunc("/submit_with_exclusions", submitWithExclusionsHandler)
	http.HandleFunc("/fetch", fetchHandler)
	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func loadCreditInfo(filename string) (map[string]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open credit info file %s: %w", filename, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ','
	reader.LazyQuotes = true

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read credit info CSV %s: %w", filename, err)
	}

	infoMap := make(map[string]string)
	for i, row := range rows {
		if i == 0 {
			continue
		} // Skip header
		if len(row) >= 2 {
			id := strings.TrimSpace(row[0])
			text := strings.TrimSpace(row[1])
			if id != "" {
				infoMap[id] = text
			} else {
				log.Printf("Warning: Empty ID found in %s row %d", filename, i+1)
			}
		} else {
			log.Printf("Warning: Skipping short row in %s: %v", filename, row)
		}
	}
	log.Printf("Loaded %d credit info entries from %s", len(infoMap), filename)
	return infoMap, nil
}

func loadPartyColors(filename string) (map[string]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open party colors file %s: %w", filename, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ','
	reader.LazyQuotes = true

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read party colors CSV %s: %w", filename, err)
	}

	colors := make(map[string]string)
	for i, row := range rows {
		if i == 0 {
			continue
		} // Skip header
		if len(row) >= 2 {
			partyName := strings.TrimSpace(row[0])
			color := strings.TrimSpace(row[1])
			if partyName != "" {
				colors[partyName] = color
			} else {
				log.Printf("Warning: Empty party name found in %s row %d", filename, i+1)
			}
		} else {
			log.Printf("Warning: Skipping short row in %s: %v", filename, row)
		}
	}
	log.Printf("Loaded %d party colors from %s", len(colors), filename)
	return colors, nil
}

func resultsHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("results.html")
	if err != nil {
		log.Printf("Error parsing results.html: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	err = tmpl.Execute(w, nil)
	if err != nil {
		log.Printf("Error executing results.html template: %v", err)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	agencies, err := fetchAllAgencies("PollsSeats.csv")
	if err != nil {
		log.Printf("Error fetching agencies for index: %v", err)
		http.Error(w, "Error loading poll data", http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFiles("index.html")
	if err != nil {
		log.Printf("Error parsing index.html: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	err = tmpl.Execute(w, agencies)
	if err != nil {
		log.Printf("Error executing index.html template: %v", err)
	}
}

func fetchAllAgencies(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("fetchAllAgencies: failed to open polls file %s: %w", filename, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ','
	reader.LazyQuotes = true

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("fetchAllAgencies: failed to read polls CSV %s: %w", filename, err)
	}

	type AgencyDate struct {
		Name string
		Date time.Time
	}
	agencyMap := make(map[string]time.Time)

	for i, row := range rows {
		if i == 0 {
			continue
		} // Skip header
		if len(row) < 2 {
			continue
		}

		dateStr := strings.TrimSpace(row[0])
		agencyStr := strings.TrimSpace(row[1])
		if dateStr == "" || agencyStr == "" {
			continue
		}

		date, err := time.Parse("02.01.2006", dateStr)
		if err != nil {
			// Log parse error but continue processing other rows
			log.Printf("Warning: fetchAllAgencies: Error parsing date '%s': %v. Skipping row.", dateStr, err)
			continue
		}
		agencyWithDate := agencyStr + " - " + dateStr
		if _, exists := agencyMap[agencyWithDate]; !exists {
			agencyMap[agencyWithDate] = date
		}
	}

	if len(agencyMap) == 0 {
		log.Printf("Warning: fetchAllAgencies: No valid agency/date combinations found in %s", filename)
		return []string{}, nil
	}

	var agencies []AgencyDate
	for name, date := range agencyMap {
		agencies = append(agencies, AgencyDate{Name: name, Date: date})
	}
	sort.Slice(agencies, func(i, j int) bool { return agencies[i].Date.After(agencies[j].Date) })

	var sortedAgencies []string
	for _, agency := range agencies {
		sortedAgencies = append(sortedAgencies, agency.Name)
	}
	log.Printf("fetchAllAgencies: Fetched %d unique polls for dropdown from %s", len(sortedAgencies), filename)
	return sortedAgencies, nil
}

func processRequest(r *http.Request, exclusionPairs []ExclusionPair) ([]map[string]interface{}, error) {
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("processRequest: failed to parse form: %w", err)
	}
	partiesJSON := r.FormValue("parties")
	if partiesJSON == "" {
		return nil, fmt.Errorf("processRequest: missing 'parties' data in form")
	}

	var parties []Party
	decoder := json.NewDecoder(strings.NewReader(partiesJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parties); err != nil {
		return nil, fmt.Errorf("processRequest: failed to decode parties JSON: %w", err)
	}
	if len(parties) == 0 {
		return []map[string]interface{}{}, nil
	}

	const majorityTarget = 76
	combinations := findCombinations(parties, majorityTarget, exclusionPairs)

	var chartData []map[string]interface{}
	for _, combination := range combinations {
		labels := make([]string, len(combination))
		values := make([]int, len(combination))
		colors := make([]string, len(combination))
		totalSeats := 0
		for j, party := range combination {
			labels[j] = party.Name
			values[j] = party.Seats
			colors[j] = party.Color
			totalSeats += party.Seats
		}
		chartData = append(chartData, map[string]interface{}{
			"labels": labels, "values": values, "colors": colors, "totalSeats": totalSeats,
		})
	}
	sort.Slice(chartData, func(i, j int) bool {
		lenI := len(chartData[i]["labels"].([]string))
		lenJ := len(chartData[j]["labels"].([]string))
		if lenI != lenJ {
			return lenI < lenJ
		}
		return chartData[i]["totalSeats"].(int) < chartData[j]["totalSeats"].(int)
	})
	return chartData, nil
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	chartData, err := processRequest(r, []ExclusionPair{})
	if err != nil {
		log.Printf("Error processing submit request: %v", err)
		if strings.Contains(err.Error(), "decode") || strings.Contains(err.Error(), "missing") {
			http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(chartData); err != nil {
		log.Printf("Error encoding chart data response: %v", err)
	}
}

func submitWithExclusionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request: Could not parse form data", http.StatusBadRequest)
		return
	}
	exclusionJSON := r.FormValue("exclusions")
	if exclusionJSON == "" {
		http.Error(w, "Bad Request: Missing 'exclusions' data", http.StatusBadRequest)
		return
	}
	var exclusionPairs []ExclusionPair
	decoder := json.NewDecoder(strings.NewReader(exclusionJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&exclusionPairs); err != nil {
		http.Error(w, fmt.Sprintf("Bad Request: Invalid 'exclusions' JSON: %v", err), http.StatusBadRequest)
		return
	}
	log.Println("Received Exclusions: ", exclusionPairs)

	chartData, err := processRequest(r, exclusionPairs)
	if err != nil {
		log.Printf("Error processing submit request with exclusions: %v", err)
		if strings.Contains(err.Error(), "decode") || strings.Contains(err.Error(), "missing") {
			http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(chartData); err != nil {
		log.Printf("Error encoding chart data response with exclusions: %v", err)
	}
}

func exclusionsHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("exclusions.html")
	if err != nil {
		log.Printf("Error parsing exclusions.html: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	err = tmpl.Execute(w, nil)
	if err != nil {
		log.Printf("Error executing exclusions.html template: %v", err)
	}
}

func containsExclusionPairs(combination []Party, exclusionPairs []ExclusionPair) bool {
	if len(exclusionPairs) == 0 {
		return false
	}
	partyNamesInCombination := make(map[string]struct{}, len(combination))
	for _, party := range combination {
		partyNamesInCombination[party.Name] = struct{}{}
	}
	for _, pair := range exclusionPairs {
		_, firstExists := partyNamesInCombination[pair.FirstParty]
		_, secondExists := partyNamesInCombination[pair.SecondParty]
		if firstExists && secondExists {
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

func findCombinationsRec(availableParties []Party, targetSeats, currentSum int, currentCombination []Party, result *[][]Party, exclusionPairs []ExclusionPair) {
	if currentSum >= targetSeats {
		if !containsExclusionPairs(currentCombination, exclusionPairs) {
			combinationCopy := make([]Party, len(currentCombination))
			copy(combinationCopy, currentCombination)
			*result = append(*result, combinationCopy)
		}
		return
	}
	if len(availableParties) == 0 {
		return
	}
	party := availableParties[0]
	remainingParties := availableParties[1:]
	findCombinationsRec(remainingParties, targetSeats, currentSum+party.Seats, append(currentCombination, party), result, exclusionPairs) // Include
	findCombinationsRec(remainingParties, targetSeats, currentSum, currentCombination, result, exclusionPairs)                            // Exclude
}

// fetchHandler - Handles GET request to fetch party data for a specific poll source
func fetchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	filename := "PollsSeats.csv"
	sourceWithDate := r.URL.Query().Get("source")
	if sourceWithDate == "" {
		http.Error(w, "Bad Request: Missing 'source' query parameter", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(sourceWithDate, " - ", 2)
	if len(parts) != 2 {
		log.Printf("ERROR: fetchHandler: Invalid source format received: '%s'", sourceWithDate)
		http.Error(w, "Bad Request: Invalid 'source' format. Expected 'Agency Name - DD.MM.YYYY'", http.StatusBadRequest)
		return
	}
	agencyFilter := strings.TrimSpace(parts[0])
	dateFilter := strings.TrimSpace(parts[1])

	log.Printf("INFO: fetchHandler: Received request for Agency: '%s', Date: '%s'", agencyFilter, dateFilter)

	parties, actualDate, creditInfoText, err := fetchAndFilterParties(filename, agencyFilter, dateFilter)
	if err != nil {
		// Log the underlying error from fetchAndFilterParties
		log.Printf("ERROR: fetchHandler: Error calling fetchAndFilterParties for source '%s': %v", sourceWithDate, err)
		http.Error(w, "Internal Server Error: Could not retrieve poll data", http.StatusInternalServerError)
		return
	}

	if len(parties) == 0 {
		// This is not necessarily an error, just no data found for the specific filter
		log.Printf("INFO: fetchHandler: No parties found for Agency: '%s', Date: '%s'. Returning empty result.", agencyFilter, dateFilter)
	} else {
		log.Printf("INFO: fetchHandler: Found %d parties for Agency: '%s', Date: '%s'.", len(parties), agencyFilter, dateFilter)
	}

	result := map[string]interface{}{
		"parties": parties, "date": actualDate, "creditInfo": creditInfoText,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // Send OK status even if parties array is empty
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("ERROR: fetchHandler: Error encoding fetch response for source '%s': %v", sourceWithDate, err)
	}
}

// fetchAndFilterParties - Fetches and filters parties based on separate agency and date strings.
func fetchAndFilterParties(filename string, agencyFilter string, dateFilter string) ([]Party, string, string, error) {
	file, err := os.Open(filename)
	if err != nil {
		// Add context to the error
		return nil, "", "", fmt.Errorf("fetchAndFilterParties: failed to open polls file '%s': %w", filename, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ','
	reader.LazyQuotes = true
	// Add explicit field count check?
	// reader.FieldsPerRecord = 5 // Set this if you expect exactly 5 fields per row (excluding header)

	rows, err := reader.ReadAll()
	if err != nil {
		// Check for specific CSV errors if possible
		if parseErr, ok := err.(*csv.ParseError); ok {
			log.Printf("ERROR: fetchAndFilterParties: CSV Parse Error in '%s' at line %d, column %d: %v", filename, parseErr.Line, parseErr.Column, parseErr.Err)
		}
		return nil, "", "", fmt.Errorf("fetchAndFilterParties: failed to read polls CSV '%s': %w", filename, err)
	}

	log.Printf("DEBUG: fetchAndFilterParties: Read %d rows (including header) from %s", len(rows), filename)

	var parties []Party
	var actualDateFound string
	var creditInfoId string
	var creditInfoText string = "Zdroj neuvedený" // Default
	headerSkipped := false
	firstMatchFound := false
	rowsChecked := 0
	rowsMatched := 0

	for i, row := range rows { // Add index 'i' for logging row number
		if !headerSkipped {
			headerSkipped = true
			continue
		}
		rowsChecked++

		if len(row) < 5 {
			log.Printf("Warning: fetchAndFilterParties: Skipping short row %d in '%s': %v", i+1, filename, row)
			continue
		}

		rowDate := strings.TrimSpace(row[0])
		rowAgency := strings.TrimSpace(row[1])
		rowPartyName := strings.TrimSpace(row[2])
		rowSeatStr := strings.TrimSpace(row[3])
		rowCreditId := strings.TrimSpace(row[4])

		// --- CORE FILTER LOGIC ---
		// Add debugging logs right before the comparison
		// log.Printf("DEBUG: Comparing: Row %d: ('%s' == '%s') && ('%s' == '%s')", i+1, rowAgency, agencyFilter, rowDate, dateFilter)

		if rowAgency == agencyFilter && rowDate == dateFilter {
			rowsMatched++
			// log.Printf("DEBUG: Match found at row %d!", i+1) // Log when a match occurs

			if !firstMatchFound {
				actualDateFound = rowDate
				if rowCreditId != "" {
					creditInfoId = rowCreditId
				} else { /* Log warning if needed */
				}
				firstMatchFound = true
			}

			seats, err := strconv.Atoi(rowSeatStr)
			if err != nil {
				log.Printf("Warning: fetchAndFilterParties: Error converting seats '%s' for party '%s' (Row %d, Agency '%s', Date '%s'): %v. Skipping party.", rowSeatStr, rowPartyName, i+1, agencyFilter, dateFilter, err)
				continue
			}
			if seats <= 0 {
				continue
			} // Skip 0 seat entries

			color, ok := partyColors[rowPartyName]
			if !ok {
				color = "#808080" /* Log warning if needed */
			}

			party := Party{Name: rowPartyName, Seats: seats, Color: color}
			parties = append(parties, party)
		}
		// --- END FILTER ---
	}

	log.Printf("DEBUG: fetchAndFilterParties: Checked %d data rows, Matched %d rows for Agency '%s', Date '%s'. Found %d valid parties.", rowsChecked, rowsMatched, agencyFilter, dateFilter, len(parties))

	if firstMatchFound && creditInfoId != "" { // Only look up if we found a match and got an ID
		text, ok := creditInfoMap[creditInfoId]
		if ok {
			creditInfoText = text
		} else {
			log.Printf("Warning: fetchAndFilterParties: Credit info text not found in creditInfoMap for ID '%s' (Agency '%s', Date '%s'). Using default.", creditInfoId, agencyFilter, dateFilter)
		}
	} else if firstMatchFound { // Found match(es) but no ID
		log.Printf("Info: fetchAndFilterParties: No creditInfoId found in matching CSV rows for Agency '%s', Date '%s'. Using default credit text.", agencyFilter, dateFilter)
	}
	// If !firstMatchFound, the default "Zdroj neuvedený" is kept.

	return parties, actualDateFound, creditInfoText, nil
}
