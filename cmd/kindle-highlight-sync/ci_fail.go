package main

import "os"

func ciFailLint() {
	os.Remove("nonexistent")
}
