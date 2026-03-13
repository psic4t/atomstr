package main

import (
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

func TestParseFeedDate(t *testing.T) {
	// Test case 1: Feed item with PublishedParsed
	item1 := &gofeed.Item{
		Title:           "Test Item 1",
		PublishedParsed: &time.Time{},
	}
	*item1.PublishedParsed = time.Date(2023, 10, 30, 12, 0, 0, 0, time.UTC)

	parsedTime, err := parseFeedDate(item1)
	if err != nil {
		t.Errorf("Expected success for item1, got error: %v", err)
	}
	if !parsedTime.Equal(*item1.PublishedParsed) {
		t.Errorf("Expected PublishedParsed time, got: %v", parsedTime)
	}

	// Test case 2: Feed item with only UpdatedParsed
	item2 := &gofeed.Item{
		Title:         "Test Item 2",
		UpdatedParsed: &time.Time{},
	}
	*item2.UpdatedParsed = time.Date(2023, 10, 30, 12, 0, 0, 0, time.UTC)

	parsedTime, err = parseFeedDate(item2)
	if err != nil {
		t.Errorf("Expected success for item2, got error: %v", err)
	}
	if !parsedTime.Equal(*item2.UpdatedParsed) {
		t.Errorf("Expected UpdatedParsed time, got: %v", parsedTime)
	}

	// Test case 3: Feed item with only Published string
	item3 := &gofeed.Item{
		Title:     "Test Item 3",
		Published: "2023-10-30T12:00:00Z",
	}

	parsedTime, err = parseFeedDate(item3)
	if err != nil {
		t.Errorf("Expected success for item3, got error: %v", err)
	}
	expectedTime, _ := time.Parse(time.RFC3339, "2023-10-30T12:00:00Z")
	if !parsedTime.Equal(expectedTime) {
		t.Errorf("Expected parsed time %v, got: %v", expectedTime, parsedTime)
	}

	// Test case 4: Feed item with NL Times style date (e.g. "13 March 2026 - 22:00")
	item4 := &gofeed.Item{
		Title:     "Test Item 4",
		Published: "13 March 2026 - 22:00",
	}

	parsedTime, err = parseFeedDate(item4)
	if err != nil {
		t.Errorf("Expected success for item4, got error: %v", err)
	}
	expectedNLTime := time.Date(2026, time.March, 13, 22, 0, 0, 0, time.UTC)
	if !parsedTime.Equal(expectedNLTime) {
		t.Errorf("Expected parsed time %v, got: %v", expectedNLTime, parsedTime)
	}

	// Test case 5: Feed item with ISO8601 date with space before T (e.g. "2024-06-04 T14:00:00+09:00")
	item5 := &gofeed.Item{
		Title:     "Test Item 5",
		Published: "2024-06-04 T14:00:00+09:00",
	}

	parsedTime, err = parseFeedDate(item5)
	if err != nil {
		t.Errorf("Expected success for item5, got error: %v", err)
	}
	expectedSpaceTTime := time.Date(2024, time.June, 4, 14, 0, 0, 0, time.FixedZone("", 9*60*60))
	if !parsedTime.Equal(expectedSpaceTTime) {
		t.Errorf("Expected parsed time %v, got: %v", expectedSpaceTTime, parsedTime)
	}

	// Test case 6: Feed item with RSS date without time (e.g. "Thu, 12 Aug 2021 UTC")
	item6 := &gofeed.Item{
		Title:     "Test Item 6",
		Published: "Thu, 12 Aug 2021 UTC",
	}

	parsedTime, err = parseFeedDate(item6)
	if err != nil {
		t.Errorf("Expected success for item6, got error: %v", err)
	}
	expectedNoTime := time.Date(2021, time.August, 12, 0, 0, 0, 0, time.UTC)
	if !parsedTime.Equal(expectedNoTime) {
		t.Errorf("Expected parsed time %v, got: %v", expectedNoTime, parsedTime)
	}

	// Test case 7: Feed item with no date info
	item7 := &gofeed.Item{
		Title: "Test Item 7",
	}

	_, err = parseFeedDate(item7)
	if err == nil {
		t.Error("Expected error for item7 with no date info")
	}
}
