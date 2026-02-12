# PET-1238 Web Console 界面优化 - 实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 优化Web Console列表页的分页、表格列配置和查询过滤功能，提升用户体验。

**Architecture:**
- 后端：增强GetPages()函数支持灵活分页显示，为每个列表页定义可搜索字段
- 前端：创建通用分页组件（支持页数跳转、每页数量切换）、表格列配置组件（基于localStorage）、统一的查询过滤框架
- 数据流：URL参数传递limit、offset、filter_fields；localStorage存储列显示偏好

**Tech Stack:**
- Backend: Go, Macaron, GORM
- Frontend: jQuery, Semantic UI, localStorage
- Templates: Go text/html templates

---

## 任务分解

### Task 1: 增强分页函数和数据结构

**Files:**
- Modify: `web/src/routes/page.go`

**Step 1: 理解当前GetPages函数的限制**

Current function in `web/src/routes/page.go:8-22` generates ALL page links, which causes overflow for large datasets.
- 需要支持只显示部分页码（如当前页前后各5页）
- 需要支持"上一页/下一页"按钮
- 需要支持总页数显示

**Step 2: 扩展Page结构体**

```go
type Page struct {
	Number     int
	Offset     int
	IsCurrent  bool
	IsEllipsis bool // for "..." display
}

type PageInfo struct {
	Pages        []*Page
	TotalPages   int
	CurrentPage  int
	HasPrevious  bool
	HasNext      bool
	PreviousPage int
	NextPage     int
}
```

**Step 3: 编写新的GetPaginationInfo函数**

```go
// GetPaginationInfo returns pagination information with smart page range display
// Shows 5 pages before and after current page, with ellipsis for gaps
func GetPaginationInfo(total, limit, offset int64) (pageInfo *PageInfo) {
	if limit == 0 {
		limit = 16
	}

	totalPages := (total + limit - 1) / limit // ceiling division
	currentPage := (offset / limit) + 1

	pageInfo = &PageInfo{
		Pages:       []*Page{},
		TotalPages:  int(totalPages),
		CurrentPage: int(currentPage),
		HasPrevious: currentPage > 1,
		HasNext:     currentPage < int(totalPages),
	}

	if pageInfo.HasPrevious {
		pageInfo.PreviousPage = int(currentPage - 1)
	}
	if pageInfo.HasNext {
		pageInfo.NextPage = int(currentPage + 1)
	}

	// Generate page list with smart range (5 before, 5 after current)
	startPage := int(currentPage) - 5
	endPage := int(currentPage) + 5

	if startPage < 1 {
		startPage = 1
	}
	if endPage > int(totalPages) {
		endPage = int(totalPages)
	}

	// Add page 1 if not in range
	if startPage > 1 {
		page := &Page{Number: 1, Offset: 0, IsCurrent: false}
		pageInfo.Pages = append(pageInfo.Pages, page)
		if startPage > 2 {
			pageInfo.Pages = append(pageInfo.Pages, &Page{IsEllipsis: true})
		}
	}

	// Add pages in range
	for p := startPage; p <= endPage; p++ {
		offset := int64((p - 1) * int(limit))
		page := &Page{
			Number:    p,
			Offset:    int(offset),
			IsCurrent: p == int(currentPage),
		}
		pageInfo.Pages = append(pageInfo.Pages, page)
	}

	// Add last page if not in range
	if endPage < int(totalPages) {
		if endPage < int(totalPages)-1 {
			pageInfo.Pages = append(pageInfo.Pages, &Page{IsEllipsis: true})
		}
		page := &Page{
			Number:    int(totalPages),
			Offset:    int((int(totalPages) - 1) * int(limit)),
			IsCurrent: false,
		}
		pageInfo.Pages = append(pageInfo.Pages, page)
	}

	return
}

// KeepGetPages for backward compatibility
func GetPages(total, limit int64) (pages []*Page) {
	pageInfo := GetPaginationInfo(total, limit, 0)
	return pageInfo.Pages
}
```

**Step 4: 提交更改**

```bash
git add web/src/routes/page.go
git commit -m "feat: enhance pagination with smart page range display and navigation info

- Add PageInfo structure to support pagination metadata
- Implement GetPaginationInfo() for smart page display (5 before/after current)
- Support ellipsis for page gaps, previous/next navigation
- Maintain backward compatibility with GetPages()
"
```

---

### Task 2: 创建每页数量和列配置的通用结构

**Files:**
- Create: `web/src/routes/config.go`

**Step 1: 定义可搜索字段配置结构**

```go
package routes

// FilterField defines a filterable field for list views
type FilterField struct {
	Name        string // database column name
	Label       string // UI label
	Type        string // 'text', 'select', 'date', 'number'
	Options     []string // for 'select' type
	Placeholder string
}

// ListConfig defines the configuration for a list view
type ListConfig struct {
	Name         string        // list name (e.g., 'zones', 'instances')
	FilterFields []FilterField // available filter fields
	DefaultLimit int64         // default items per page
	PageSizes    []int64       // available page sizes [10, 20, 50, 100]
}

// GlobalListConfigs maps list name to its configuration
var GlobalListConfigs = map[string]*ListConfig{
	"zones": {
		Name: "zones",
		FilterFields: []FilterField{
			{Name: "id", Label: "ID", Type: "number"},
			{Name: "name", Label: "Name", Type: "text", Placeholder: "Zone name"},
		},
		DefaultLimit: 16,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"hypers": {
		Name: "hypers",
		FilterFields: []FilterField{
			{Name: "id", Label: "ID", Type: "number"},
			{Name: "hostname", Label: "Hostname", Type: "text", Placeholder: "Hypervisor hostname"},
		},
		DefaultLimit: 16,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	"instances": {
		Name: "instances",
		FilterFields: []FilterField{
			{Name: "id", Label: "ID", Type: "number"},
			{Name: "hostname", Label: "Hostname", Type: "text", Placeholder: "Instance hostname"},
		},
		DefaultLimit: 16,
		PageSizes:    []int64{10, 20, 50, 100},
	},
	// TODO: Add more list configurations
}

// GetListConfig returns configuration for a specific list
func GetListConfig(listName string) *ListConfig {
	if config, ok := GlobalListConfigs[listName]; ok {
		return config
	}
	// Return default config if not found
	return &ListConfig{
		DefaultLimit: 16,
		PageSizes:    []int64{10, 20, 50, 100},
	}
}
```

**Step 2: 提交更改**

```bash
git add web/src/routes/config.go
git commit -m "feat: add list configuration structure for filters and display options

- Define FilterField structure for searchable fields
- Define ListConfig for list-specific configuration
- Initialize GlobalListConfigs with zones, hypers, instances
- Add GetListConfig() helper function
"
```

---

### Task 3: 创建分页和列配置的模板组件

**Files:**
- Create: `web/templates/_pagination.tmpl`
- Create: `web/templates/_column_selector.tmpl`

**Step 1: 创建分页模板组件**

```html
<!-- _pagination.tmpl -->
<!-- Pagination component with page size selector and jump to page -->
{{ if .PageInfo }}
<div class="ui attached segment">
	<div class="ui stackable grid">
		<!-- Page size selector -->
		<div class="four wide column">
			<form method="get" id="pageSizeForm" class="ui form">
				<div class="field">
					<label>{{.i18n.Tr "Items_Per_Page"}}:</label>
					<select name="limit" onchange="document.getElementById('pageSizeForm').submit()">
						{{ range .PageInfo.PageSizes }}
							<option value="{{.}}" {{ if eq . $.Limit }}selected{{ end }}>{{.}}</option>
						{{ end }}
					</select>
				</div>
				<!-- preserve other query parameters -->
				{{ if .Query }}<input type="hidden" name="q" value="{{.Query}}">{{ end }}
			</form>
		</div>

		<!-- Page jump -->
		<div class="four wide column">
			<form method="get" id="pageJumpForm" class="ui form">
				<div class="field">
					<label>{{.i18n.Tr "Go_To_Page"}}:</label>
					<div class="ui action input">
						<input type="number" name="page" min="1" max="{{.PageInfo.TotalPages}}" placeholder="1">
						<button class="ui button" type="submit">Go</button>
					</div>
				</div>
				{{ if .Query }}<input type="hidden" name="q" value="{{.Query}}">{{ end }}
				<input type="hidden" name="limit" value="{{.Limit}}">
			</form>
		</div>

		<!-- Pagination menu -->
		<div class="eight wide column">
			{{ if .PageInfo.Pages }}
			<div class="ui pagination menu">
				<!-- Previous button -->
				{{ if .PageInfo.HasPrevious }}
					<a class="item" href="{{ .Link }}?offset={{mul (sub .PageInfo.PreviousPage 1) .Limit}}&limit={{.Limit}}{{ if .Query }}&q={{.Query}}{{ end }}">
						<i class="left chevron icon"></i>
					</a>
				{{ end }}

				<!-- Page links -->
				{{ range .PageInfo.Pages }}
					{{ if .IsEllipsis }}
						<div class="disabled item">...</div>
					{{ else if .IsCurrent }}
						<a class="active item">{{.Number}}</a>
					{{ else }}
						<a class="item" href="{{ $.Link }}?offset={{.Offset}}&limit={{$.Limit}}{{ if $.Query }}&q={{$.Query}}{{ end }}">{{.Number}}</a>
					{{ end }}
				{{ end }}

				<!-- Next button -->
				{{ if .PageInfo.HasNext }}
					<a class="item" href="{{ .Link }}?offset={{mul .PageInfo.NextPage .Limit}}&limit={{.Limit}}{{ if .Query }}&q={{.Query}}{{ end }}">
						<i class="right chevron icon"></i>
					</a>
				{{ end }}
			</div>
			<p class="ui small text">{{.i18n.Tr "Total"}}: {{.Total}} | {{.i18n.Tr "Page"}}: {{.PageInfo.CurrentPage}}/{{.PageInfo.TotalPages}}</p>
			{{ end }}
		</div>
	</div>
</div>
{{ end }}
```

**Step 2: 创建列配置模板组件**

```html
<!-- _column_selector.tmpl -->
<!-- Column visibility selector with localStorage support -->
<script type="text/javascript">
(function() {
	var ListName = '{{ .ListName }}';
	var DefaultColumns = {{ .DefaultColumnsJSON }};

	// Initialize column visibility from localStorage
	function initColumnSelector() {
		var storageKey = 'columns_' + ListName;
		var savedColumns = localStorage.getItem(storageKey);
		var visibleColumns;

		if (savedColumns) {
			visibleColumns = JSON.parse(savedColumns);
		} else {
			visibleColumns = DefaultColumns;
		}

		// Apply visibility
		$('table tbody tr').each(function() {
			$(this).find('td').each(function(idx) {
				var colName = $('thead tr th').eq(idx).data('column');
				if (colName && visibleColumns.indexOf(colName) === -1) {
					$(this).hide();
				}
			});
		});

		// Hide header columns
		$('thead tr th').each(function(idx) {
			var colName = $(this).data('column');
			if (colName && visibleColumns.indexOf(colName) === -1) {
				$(this).hide();
			}
		});

		// Update checkbox status
		$('input[name="columns"]').each(function() {
			$(this).prop('checked', visibleColumns.indexOf($(this).value) !== -1);
		});
	}

	// Save column preference and reload
	function saveColumnPreference() {
		var storageKey = 'columns_' + ListName;
		var selected = [];
		$('input[name="columns"]:checked').each(function() {
			selected.push($(this).value);
		});
		localStorage.setItem(storageKey, JSON.stringify(selected));
		location.reload();
	}

	// Reset to default columns
	function resetColumns() {
		var storageKey = 'columns_' + ListName;
		localStorage.removeItem(storageKey);
		location.reload();
	}

	// Export functions to window for HTML onclick handlers
	window.saveColumnPreference = saveColumnPreference;
	window.resetColumns = resetColumns;
	window.initColumnSelector = initColumnSelector;

	// Initialize on page load
	$(document).ready(function() {
		initColumnSelector();
	});
})();
</script>

<!-- Column selector modal/dropdown -->
<div class="ui column-selector-dropdown item">
	<i class="columns icon"></i> {{.i18n.Tr "Column_Settings"}}
	<div class="menu">
		{{ range .AvailableColumns }}
		<label class="item">
			<input type="checkbox" name="columns" value="{{.}}" class="column-checkbox">
			{{.}}
		</label>
		{{ end }}
		<div class="divider"></div>
		<a class="item" onclick="saveColumnPreference()">
			<i class="check icon"></i> {{.i18n.Tr "Apply"}}
		</a>
		<a class="item" onclick="resetColumns()">
			<i class="redo icon"></i> {{.i18n.Tr "Reset_Default"}}
		</a>
	</div>
</div>
```

**Step 3: 提交更改**

```bash
git add web/templates/_pagination.tmpl web/templates/_column_selector.tmpl
git commit -m "feat: add pagination and column selector template components

- Create _pagination.tmpl with page size selector, jump to page, smart page navigation
- Create _column_selector.tmpl with localStorage-based column visibility
- Support column preference persistence across page reloads
"
```

---

### Task 4: 更新zones列表页实现

**Files:**
- Modify: `web/src/routes/zone.go:202-233` (List function)
- Modify: `web/templates/zones.tmpl`

**Step 1: 增强ZoneView.List函数以支持新的参数**

```go
func (v *ZoneView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}

	// Get list configuration
	listConfig := GetListConfig("zones")

	// Get pagination parameters
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = listConfig.DefaultLimit
	}

	// Validate limit
	validLimit := false
	for _, size := range listConfig.PageSizes {
		if limit == size {
			validLimit = true
			break
		}
	}
	if !validLimit {
		limit = listConfig.DefaultLimit
	}

	// Handle page jump (page parameter takes precedence)
	if page := c.QueryInt64("page"); page > 0 {
		offset = (page - 1) * limit
	}

	// Get search query
	order := c.Query("order")
	if order == "" {
		order = "name"
	}
	query := c.QueryTrim("q")

	// Fetch data
	total, zones, err := zoneAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}

	// Get pagination info
	pageInfo := GetPaginationInfo(total, limit, offset)
	pageInfo.PageSizes = listConfig.PageSizes

	// Set template data
	c.Data["Zones"] = zones
	c.Data["Total"] = total
	c.Data["PageInfo"] = pageInfo
	c.Data["Limit"] = limit
	c.Data["Query"] = query
	c.Data["ListConfig"] = listConfig
	c.Data["ListName"] = "zones"
	c.Data["DefaultColumnsJSON"] = `["ID", "Name", "CreatedAt", "UpdatedAt", "Remark", "Action"]`
	c.Data["AvailableColumns"] = []string{"ID", "Name", "Default", "CreatedAt", "UpdatedAt", "Remark", "Action"}
	c.HTML(200, "zones")
}
```

**Step 2: 更新zones.tmpl模板使用新组件**

Replace the pagination section in zones.tmpl (lines 50-60) and add column selector:

```html
{{template "_head" .}}
    <div class="admin user">
	    <div class="ui container">
		    <div class="ui grid">
                {{template "_left" .}}
          	    <div class="twelve wide column content">
		            <h4 class="ui top attached header">
			            {{.i18n.Tr "Zone_Manage_Panel"}} ({{.i18n.Tr "Total"}}: {{.Total}})
			            <div class="ui right">
				            {{template "_column_selector" .}}
				            <a class="ui green tiny button" href="zones/new">{{.i18n.Tr "Create"}}</a>
			            </div>
		            </h4>
		            <div class="ui attached segment">
			            <form class="ui form">
	                        <div class="ui fluid tiny action input">
	                            <input name="q" value="{{ .Query }}" placeholder="Search..." autofocus>
	                            <button class="ui blue tiny button">{{.i18n.Tr "Search"}}</button>
	                        </div>
                        </form>
		            </div>
		            <div class="ui unstackable attached table segment">
                        <table class="ui unstackable very basic striped table">
	                        <thead>
		                        <tr>
			                        <th data-column="ID">{{.i18n.Tr "ID"}}</th>
			                        <th data-column="Name">{{.i18n.Tr "Name"}}</th>
			                        <th data-column="CreatedAt">{{.i18n.Tr "Created_At"}}</th>
			                        <th data-column="UpdatedAt">{{.i18n.Tr "Updated_At"}}</th>
			                        <th data-column="Remark">{{.i18n.Tr "Remark"}}</th>
                                    <th data-column="Action">{{.i18n.Tr "Action"}}</th>
		                        </tr>
	                        </thead>
	                        <tbody>
                                {{ $Link := .Link }}
                                {{ range .Zones }}
		                        <tr>
									<td><a href="{{$Link}}/{{.ID}}/edit">{{.ID}}</a></td>
			                        <td>{{.Name}}</td>
			                        <td><span title="{{.CreatedAt}}">{{.CreatedAt}}</span></td>
			                        <td><span title="{{.UpdatedAt}}">{{.UpdatedAt}}</span></td>
			                        <td>{{.Remark}}</td>
									<td><div class="delete-button" data-url="{{$Link}}/{{.ID}}" data-id="{{.ID}}"><i class="dark purple trash alternate outline icon"></i></div></td>
								</tr>
                                {{ end }}
	                        </tbody>
                        </table>
		            </div>
		            {{template "_pagination" .}}
	            </div>
            </div>
        </div>
    </div>

    <div class="ui small basic delete modal">
	    <div class="ui icon header">
		    <i class="trash icon"></i>
            {{.i18n.Tr "Zone Deletion"}}
	    </div>
	    <div class="content">
		    <p>{{.i18n.Tr "Zone_Deletion_Confirm"}}</p>
	    </div>
	    {{template "_delete_modal_actions" .}}
    </div>

{{template "_footer" .}}
```

**Step 3: 提交更改**

```bash
git add web/src/routes/zone.go web/templates/zones.tmpl
git commit -m "feat: update zones list with pagination and column configuration

- Support configurable page sizes (10, 20, 50, 100)
- Add page jump functionality
- Integrate column selector component
- Pass list configuration to template
"
```

---

### Task 5: 更新instances列表页实现（主要列表页示例）

**Files:**
- Modify: `web/src/routes/instance.go` (List function - large file, focus on List method)
- Modify: `web/templates/instances.tmpl`

**Step 1: 找到并更新instanceView.List函数**

Search for the List function in instance.go and update it similar to zone.go:

```go
// Update the List function in InstanceView to:
// 1. Get listConfig from GetListConfig("instances")
// 2. Parse limit and page parameters
// 3. Validate limit against allowed page sizes
// 4. Call GetPaginationInfo instead of GetPages
// 5. Set PageInfo, Limit, ListConfig, ListName, AvailableColumns in c.Data
```

Reference the zone.go List function implementation from Task 4.

**Step 2: 更新instances.tmpl模板**

Similar to Task 4 Step 2:
1. Replace GetPages pagination section with _pagination template
2. Add _column_selector to the header
3. Add data-column attributes to table headers
4. Adjust template data variables to match new structure

**Step 3: 提交更改**

```bash
git add web/src/routes/instance.go web/templates/instances.tmpl
git commit -m "feat: update instances list with pagination and column configuration

- Support configurable page sizes
- Add page jump and column selector
- Maintain existing instance-specific features
"
```

---

### Task 6: 创建国际化资源条目

**Files:**
- Modify: `web/conf/locale/en-US.ini`
- Modify: `web/conf/locale/zh-CN.ini`

**Step 1: 为英文添加新的国际化条目**

在 `web/conf/locale/en-US.ini` 中添加:

```ini
Items_Per_Page = Items Per Page
Go_To_Page = Jump to Page
Column_Settings = Column Settings
Apply = Apply
Reset_Default = Reset to Default
Page = Page
```

**Step 2: 为中文添加新的国际化条目**

在 `web/conf/locale/zh-CN.ini` 中添加:

```ini
Items_Per_Page = 每页项数
Go_To_Page = 跳转到页
Column_Settings = 列设置
Apply = 应用
Reset_Default = 恢复默认
Page = 页
```

**Step 3: 提交更改**

```bash
git add web/conf/locale/en-US.ini web/conf/locale/zh-CN.ini
git commit -m "i18n: add translations for pagination and column configuration

- Add English translations for new UI elements
- Add Chinese translations for new UI elements
"
```

---

### Task 7: 为所有其他列表页集成新功能

**Files:**
- Modify: `web/src/routes/hyper.go`, `user.go`, `org.go`, `flavor.go`, `image.go`, etc. (all *View.List functions)
- Modify: All corresponding `web/templates/*.tmpl` files

**Step 1: 识别所有列表页**

From routes.go, identify all `.List` routes:
- zones, hypers, users, orgs, instances, flavors, images, volumes, subnets, ipgroups, keys, floatingips, loadbalancers, routers, secgroups, backups, cgroups, tasks, etc.

**Step 2: 更新所有View.List函数**

为每个列表页在 config.go 中添加 ListConfig（Task 2）。
然后更新每个对应的 List 函数，使用与 zone.go 相同的模式。

**Step 3: 更新所有对应的模板文件**

为每个模板：
1. 在分页部分使用 `{{template "_pagination" .}}`
2. 在表头添加 `{{template "_column_selector" .}}`
3. 在table headings添加 `data-column="ColumnName"`

**Step 4: 分别为每个文件提交**

```bash
# For each file pair:
git add web/src/routes/<name>.go web/templates/<name>.tmpl
git commit -m "feat: add pagination and column configuration to <name> list

- Support configurable page sizes
- Add column selector with localStorage
- Improve pagination display with smart page range
"
```

---

### Task 8: 添加JavaScript工具函数到cloudland.js

**Files:**
- Modify: `web/public/js/cloudland.js`

**Step 1: 添加URL参数处理工具函数**

在 cloudland.js 中添加：

```javascript
// URL parameter helper functions
function getUrlParam(name) {
    var url = window.location.href;
    var regex = new RegExp('[?&]' + name + '=([^&#]*)', 'i');
    var match = regex.exec(url);
    return match ? decodeURIComponent(match[1]) : '';
}

function setUrlParam(name, value) {
    var url = new URL(window.location);
    url.searchParams.set(name, value);
    window.history.replaceState({}, '', url);
}

// Column visibility helper functions for localStorage
function getColumnVisibility(listName) {
    var storageKey = 'columns_' + listName;
    var saved = localStorage.getItem(storageKey);
    return saved ? JSON.parse(saved) : null;
}

function setColumnVisibility(listName, columns) {
    var storageKey = 'columns_' + listName;
    localStorage.setItem(storageKey, JSON.stringify(columns));
}

// Pagination form helpers
function setPageSize(newLimit) {
    setUrlParam('limit', newLimit);
    setUrlParam('offset', '0'); // reset to first page
    document.forms.pageSizeForm.submit();
}

function jumpToPage(pageNum) {
    var limit = getUrlParam('limit') || '16';
    var offset = (pageNum - 1) * limit;
    setUrlParam('offset', offset);
    document.forms.pageJumpForm.submit();
}
```

**Step 2: 提交更改**

```bash
git add web/public/js/cloudland.js
git commit -m "feat: add JavaScript utilities for pagination and column management

- Add URL parameter helper functions
- Add localStorage helpers for column visibility
- Add form submission helpers for pagination
"
```

---

### Task 9: 测试和验证

**Files:**
- Test: Manual browser testing of all list pages

**Step 1: 测试分页功能**

For each list page:
1. Verify page size selector works (10, 20, 50, 100)
2. Verify page navigation with page numbers
3. Verify "Previous/Next" buttons work correctly
4. Verify page jump input works
5. Verify query parameters persist when changing page

**Step 2: 测试列配置功能**

For each list page:
1. Click column settings icon
2. Uncheck some columns
3. Click "Apply" - verify columns hide
4. Refresh page - verify columns remain hidden (localStorage persisted)
5. Click "Reset to Default" - verify all columns shown again
6. Refresh page - verify back to default

**Step 3: 验证国际化**

1. Switch to Chinese language
2. Verify all new labels display correctly in Chinese
3. Switch back to English
4. Verify all new labels display correctly in English

**Step 4: 检查搜索过滤**

For a few list pages:
1. Verify search still works with new pagination
2. Verify search results use selected page size
3. Verify search query preserved when navigating pages

**Step 5: 提交最终测试报告**

```bash
git commit --allow-empty -m "test: verify pagination and column configuration across all list pages

Verified features:
✓ Page size selection (10, 20, 50, 100)
✓ Page navigation with smart page range
✓ Page jump functionality
✓ Column visibility toggle with localStorage
✓ i18n for English and Chinese
✓ Search filter integration
✓ Backward compatibility with existing features
"
```

---

## 架构注意事项

### 后向兼容性

- `GetPages()` 函数仍然存在，返回 `GetPaginationInfo().Pages`
- 现有的模板和代码可以继续工作
- 新的 `PageInfo` 结构包含更多元数据，但旧代码不受影响

### localStorage 策略

- 列可见性偏好存储在 localStorage 中，key为 `columns_<listname>`
- 刷新页面后恢复用户的列偏好
- 管理员可以通过"重置为默认"清除偏好

### URL 参数

保存以下参数以支持可共享链接：
- `offset`: 当前偏移量
- `limit`: 每页项数
- `q`: 搜索查询
- `order`: 排序字段

### 数据库查询优化

- 所有 List 函数应该继续使用现有的数据库查询方式
- `GetPaginationInfo()` 仅处理显示逻辑，不涉及数据库
- 后端应在必要时实施查询缓存

---

## 验收标准

- [ ] 所有列表页支持可配置的每页项数 (10, 20, 50, 100)
- [ ] 分页显示优化，不显示超过11页的页码
- [ ] 支持输入页码直接跳转
- [ ] 表格列可通过菜单配置
- [ ] 列配置在浏览器页面刷新后保持
- [ ] 添加所有新 UI 标签的国际化
- [ ] 所有列表页集成新功能
- [ ] 现有功能（搜索、删除、编辑等）仍正常工作
- [ ] 支持 ID 过滤（在各列表页的 FilterField 配置中）
