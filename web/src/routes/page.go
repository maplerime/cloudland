package routes

import "math"

// Page represents a single page in pagination
type Page struct {
	Number    int  // Page number
	Offset    int  // Offset in the data
	IsCurrent bool // Mark if this is the current page
	IsEllipsis bool // Mark if this is an ellipsis placeholder
}

// PageInfo contains pagination metadata and page information
type PageInfo struct {
	Pages        []*Page // List of pages to display
	TotalPages   int     // Total number of pages
	CurrentPage  int     // Current page number
	HasPrevious  bool    // Whether there is a previous page
	HasNext      bool    // Whether there is a next page
	PreviousPage int     // Previous page number (0 if no previous)
	NextPage     int     // Next page number (0 if no next)
}

// GetPaginationInfo generates smart pagination information with limited page range display
// It shows 5 pages before and after current page, and always includes first and last page
// Parameters: total - total number of items, limit - items per page, offset - current offset
// Returns: *PageInfo with calculated pages and navigation information
func GetPaginationInfo(total, limit, offset int64) *PageInfo {
	// Handle edge cases
	if limit <= 0 {
		limit = 1
	}

	// Calculate total pages using ceiling division
	totalPages := int(math.Ceil(float64(total) / float64(limit)))
	if totalPages < 1 {
		totalPages = 1
	}

	// Calculate current page (1-indexed)
	currentPage := int(offset/limit) + 1
	if currentPage > totalPages {
		currentPage = totalPages
	}
	if currentPage < 1 {
		currentPage = 1
	}

	// Initialize PageInfo
	info := &PageInfo{
		Pages:       make([]*Page, 0),
		TotalPages:  totalPages,
		CurrentPage: currentPage,
		HasPrevious: currentPage > 1,
		HasNext:     currentPage < totalPages,
		PreviousPage: 0,
		NextPage:    0,
	}

	// Set previous and next page numbers
	if info.HasPrevious {
		info.PreviousPage = currentPage - 1
	}
	if info.HasNext {
		info.NextPage = currentPage + 1
	}

	// If total pages is small, show all pages
	if totalPages <= 11 {
		for i := 1; i <= totalPages; i++ {
			pageOffset := int((int64(i) - 1) * limit)
			info.Pages = append(info.Pages, &Page{
				Number:     i,
				Offset:     pageOffset,
				IsCurrent:  i == currentPage,
				IsEllipsis: false,
			})
		}
		return info
	}

	// For larger page counts, show smart range: 5 before, current, 5 after, with ellipsis
	rangeStart := currentPage - 5
	rangeEnd := currentPage + 5

	// Always include page 1
	if rangeStart > 1 {
		pageOffset := 0
		info.Pages = append(info.Pages, &Page{
			Number:     1,
			Offset:     pageOffset,
			IsCurrent:  false,
			IsEllipsis: false,
		})

		// Add ellipsis if there's a gap after page 1
		if rangeStart > 2 {
			info.Pages = append(info.Pages, &Page{
				Number:     0,
				Offset:     0,
				IsCurrent:  false,
				IsEllipsis: true,
			})
		}
	}

	// Adjust range if it goes below page 1
	if rangeStart < 1 {
		rangeStart = 1
	}

	// Adjust range if it goes above last page
	if rangeEnd > totalPages {
		rangeEnd = totalPages
	}

	// Add pages in the range
	for i := rangeStart; i <= rangeEnd; i++ {
		pageOffset := int((int64(i) - 1) * limit)
		info.Pages = append(info.Pages, &Page{
			Number:     i,
			Offset:     pageOffset,
			IsCurrent:  i == currentPage,
			IsEllipsis: false,
		})
	}

	// Always include last page if not already in range
	if rangeEnd < totalPages {
		// Add ellipsis if there's a gap before last page
		if rangeEnd < totalPages-1 {
			info.Pages = append(info.Pages, &Page{
				Number:     0,
				Offset:     0,
				IsCurrent:  false,
				IsEllipsis: true,
			})
		}

		pageOffset := int((int64(totalPages) - 1) * limit)
		info.Pages = append(info.Pages, &Page{
			Number:     totalPages,
			Offset:     pageOffset,
			IsCurrent:  totalPages == currentPage,
			IsEllipsis: false,
		})
	}

	return info
}

// GetPages generates page links for backward compatibility
// It now uses GetPaginationInfo internally and returns just the pages array
func GetPages(total, limit int64) (pages []*Page) {
	if total <= limit {
		return
	}

	info := GetPaginationInfo(total, limit, 0)
	return info.Pages
}
