package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := "postgres://togetherly:TogetherlySecurePass2026!@54.146.192.20:5432/table_manager?sslmode=disable"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Database connection failed: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	query := "SELECT o.sequence_number, o.status, o.table_number, o.total_minor, o.placed_at FROM orders o ORDER BY o.placed_at DESC LIMIT 15;"
	if len(os.Args) > 1 {
		query = strings.Join(os.Args[1:], " ")
	}

	rows, err := pool.Query(ctx, query)
	if err != nil {
		fmt.Printf("SQL Error: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	// Print column headers
	fieldDescriptions := rows.FieldDescriptions()
	var headers []string
	for _, fd := range fieldDescriptions {
		headers = append(headers, fd.Name)
	}
	fmt.Println(strings.Join(headers, "\t|\t"))
	fmt.Println(strings.Repeat("-", 80))

	// Print rows
	count := 0
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			continue
		}
		var strValues []string
		for _, v := range values {
			if v == nil {
				strValues = append(strValues, "NULL")
			} else {
				strValues = append(strValues, fmt.Sprintf("%v", v))
			}
		}
		fmt.Println(strings.Join(strValues, "\t|\t"))
		count++
	}
	fmt.Printf("\n(%d rows)\n", count)
}
