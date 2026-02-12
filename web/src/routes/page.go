package routes

import "math"

// Constants for smart pagination display
const (
	minPagesForSmartDisplay = 11 // Show all pages if total <= 11
	pageRangeAroundCurrent  = 5  // Show 5 pages before and after current
)

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

// GetSmartPaginationInfo generates smart pagination information with limited page range display
// It shows pageRangeAroundCurrent pages before and after the current page, and always includes first and last page
// For datasets with <= minPagesForSmartDisplay pages, all pages are shown
// Parameters: total - total number of items, limit - items per page, offset - current offset
// Returns: *PageInfo with calculated pages (including ellipsis) and navigation information
func GetSmartPaginationInfo(total, limit, offset int64) *PageInfo {
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
	if totalPages <= minPagesForSmartDisplay {
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

	// For larger page counts, show smart range: pageRangeAroundCurrent before, current, pageRangeAroundCurrent after, with ellipsis
	rangeStart := currentPage - pageRangeAroundCurrent
	rangeEnd := currentPage + pageRangeAroundCurrent

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

// GetPages generates page links for all pages (original behavior for backward compatibility)
// This function returns ALL pages without smart range truncation or ellipsis
// Parameters: total - total number of items, limit - items per page
// Returns: []*Page with all page numbers from 1 to totalPages
// Note: IsCurrent is always false since this function doesn't have context about the current offset
func GetPages(total, limit int64) (pages []*Page) {
	if total <= limit {
		return
	}

	// Calculate total pages
	totalPages := int(math.Ceil(float64(total) / float64(limit)))
	if totalPages < 1 {
		totalPages = 1
	}

	// Generate all pages without smart pagination
	number := 0
	for start := 0; start < int(total); start += int(limit) {
		number++
		page := &Page{
			Number:     number,
			Offset:     start,
			IsCurrent:  false, // Don't calculate IsCurrent; offset context not available
			IsEllipsis: false,
		}
		pages = append(pages, page)
	}
	return
}

// GetSmartPages generates page links using smart pagination with limited range display
// This wraps GetSmartPaginationInfo() and returns just the pages array
// Parameters: total - total number of items, limit - items per page, offset - current offset
// Returns: []*Page with smart-range pages (may include ellipsis entries with Number=0)
func GetSmartPages(total, limit, offset int64) []*Page {
	pageInfo := GetSmartPaginationInfo(total, limit, offset)
	return pageInfo.Pages
}
