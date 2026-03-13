func handleDivide(w http.ResponseWriter, r *http.Request) {
	aStr := r.URL.Query().Get("a")
	bStr := r.URL.Query().Get("b")

	a, err := strconv.Atoi(aStr)
	if err != nil {
		http.Error(w, "invalid parameter: a", http.StatusBadRequest)
		return
	}
	b, err := strconv.Atoi(bStr)
	if err != nil {
		http.Error(w, "invalid parameter: b", http.StatusBadRequest)
		return
	}

	// ゼロ除算ガード
	if b == 0 {
		http.Error(w, "division by zero: b must not be 0", http.StatusBadRequest)
		return
	}

	result := a / b
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"result": result})
}