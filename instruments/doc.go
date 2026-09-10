// Package instruments maintains a normalized, searchable local catalog of
// Zerodha instruments through an injected Repository.
//
// Create a Client with NewClient and close it when it is no longer needed. A
// newly created client refreshes automatically when the repository is missing
// data or stale for the current Asia/Kolkata calendar day. Long-running
// processes can call Client.RefreshIfStale on their own schedule.
//
// The repository subpackage provides a persistent DuckDB implementation.
package instruments
