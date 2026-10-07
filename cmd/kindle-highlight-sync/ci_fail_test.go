package main

import "testing"

func TestCIFail(t *testing.T) {
	t.Fatal("CI の fail を確かめる一時 test")
}
