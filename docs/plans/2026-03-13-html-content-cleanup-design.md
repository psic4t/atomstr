# HTML Content Cleanup for RSS Feed Items

## Problem

RSS feeds like Slashdot (`rss.slashdot.org/Slashdot/slashdotMain`) embed HTML
junk in their `<description>` fields: social sharing buttons with small icon
images (Twitter, Facebook), iframe comment widgets, and "share this" divs. The
current processing pipeline in `feeds.go` uses bluemonday with `AllowImages()`
and fragile regexes that fail to strip these elements, resulting in raw HTML
fragments ("garbage") appearing in published Nostr notes.

### Root Cause

The current pipeline:

1. `bluemonday.StrictPolicy()` with `AllowImages()` strips most HTML but
   **keeps** `<img>` and `<a>` tags
2. `reInlineImg` regex (`<img.src="(http.*\.(jpg|png|gif)).*/>`) tries to
   extract image URLs but fails on:
   - Non-self-closing tags (`<img src="...">` without `/>`)
   - Extensions like `.jpeg`, `.webp`, `.svg`
   - URLs with query parameters after the extension
3. `reInlineLink` regex (`<a.href="(https.*?)" .*</a>`) fails on multi-line
   tags, different attribute orders, or `http://` URLs
4. Block elements (`<p>`, `<div>`, `<br>`) are stripped without inserting
   whitespace, causing words to run together

## Solution

Replace the bluemonday + regex pipeline with goquery-based DOM processing.
goquery is already an indirect dependency (via gofeed) so this adds no new
dependency to the module.

## Design

### New Function

```go
func htmlToPlainText(rawHTML string) string
```

This function replaces the current sanitizer + regex chain in
`processFeedPost()` (lines 313-325 of `feeds.go`).

### Processing Steps

1. **Parse** raw HTML into a goquery document
2. **Remove junk elements** by CSS selector:
   - `.share_submission` — social sharing blocks (Slashdot)
   - `iframe` — embedded widgets
   - `script`, `style` — should never appear but defensive
3. **Filter icon images** — remove `<img>` tags whose `src` matches known icon
   patterns (URLs containing `icon`, `button`, `/sd/`, or dimension patterns
   like `16x16`)
4. **Extract content images** — collect remaining `<img>` `src` URLs, remove
   the tags from the DOM
5. **Convert `<a>` tags to bare URLs** — replace each `<a href="URL">text</a>`
   with just the `href` URL
6. **Convert DOM to text** with proper whitespace:
   - Block elements (`<p>`, `<div>`, `<h1>`-`<h6>`, `<blockquote>`) →
     `\n\n` before/after
   - `<br>` → `\n`
   - `<li>` → `\n- `
   - Inline elements → keep text content
7. **Append image URLs** as bare URLs on separate lines
8. **Unescape HTML entities** via `html.UnescapeString()`
9. **Normalize whitespace** — collapse runs of 3+ newlines to 2, trim edges

### What Changes in `feeds.go`

| Component | Before | After |
|-----------|--------|-------|
| `sanitizerPolicy` (bluemonday) | StrictPolicy + AllowImages + AllowAttrs | **Removed** |
| `reInlineImg` regex | Fragile `.jpg/.png/.gif` pattern | **Removed** |
| `reInlineLink` regex | Fragile `<a>` pattern | **Removed** |
| `reNitterTelegram` | Skip title for nitter/telegram | **Kept unchanged** |
| Icon filtering | None | **New** — filter social icons by URL pattern |
| Junk element removal | None | **New** — remove by CSS selector |
| Block element → newline | None (words run together) | **New** — proper whitespace |
| `html.UnescapeString` | After regex | **Kept** — after text extraction |
| Enclosure handling | Append all enclosure URLs | **Kept unchanged** |
| `feedPost.Link` append | Appended at end | **Kept unchanged** |

### Imports

```go
import "github.com/PuerkitoBio/goquery"  // promote from indirect to direct
```

Remove `"github.com/microcosm-cc/bluemonday"` if no other code uses it.

### Link Handling

| Element | Behavior |
|---------|----------|
| `<a>` inside `.share_submission` | Removed entirely (parent stripped) |
| `<a>` elsewhere in article body | Replaced with bare `href` URL |
| `feedPost.Link` | Appended at end of note (unchanged) |

This preserves the current behavior for inline links.

### Icon Filtering Strategy

Filter by URL substring rather than image dimensions (which would require HTTP
requests):

```go
var iconPatterns = []string{"icon", "button", "share", "/sd/", "logo_"}
```

An `<img>` is considered an icon if its `src` URL (lowercased) contains any of
these substrings. These are stripped; all other images are kept as content
images.

### Example: Slashdot Before/After

**Before** (HTML garbage in output):
```
Don't Get Used To Cheap AI

AI services may not stay cheap...<p><div class="share_submission"
...><a class="slashpop" href="http://twitter.com/..."><img
src="https://a.fsdn.com/sd/twitter_icon_large.png"></a>...
</div></p><p><a href="...">Read more of this story</a> at
Slashdot.</p><iframe...></iframe>
```

**After** (clean text):
```
Don't Get Used To Cheap AI

AI services may not stay cheap...

Read more of this story at Slashdot.

https://news.slashdot.org/story/...
```

## Files Changed

- `feeds.go` — replace sanitizer+regex pipeline with `htmlToPlainText()`,
  update imports, remove unused package-level vars

## Risks

- **False positives in icon filtering**: a content image URL containing "icon"
  in a path segment could be stripped. Mitigated by keeping the pattern list
  small and specific.
- **Feed-specific junk selectors**: `.share_submission` is Slashdot-specific.
  Other feeds may have different junk patterns. The goquery approach makes it
  easy to add more selectors over time.
- **goquery overhead**: DOM parsing is heavier than regex. Acceptable since
  feeds are processed at most every few minutes, not at high throughput.
