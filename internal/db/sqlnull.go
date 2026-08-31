package db

// nullableString converts an empty string to nil so it's written as SQL
// NULL rather than an empty string for optional text columns.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullableInt converts a zero value to nil so it's written as SQL NULL
// rather than 0 for optional integer columns.
func nullableInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}
