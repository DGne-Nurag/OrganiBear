//go:build !desktop

package main

import "errors"

// Ohne -tags desktop gibt es kein eigenes Fenster, nur den Browser-Modus.
// So bleiben Tests und selbst gebaute Programme ohne CGO und ohne WebKit.
const windowAvailable = false

func runWindow(*Server) error { return errors.New("diese Version hat kein eigenes Fenster") }
