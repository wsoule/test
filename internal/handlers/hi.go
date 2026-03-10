package handlers

import (
	"fmt"
	"net/http"
	"strings"
)

const hiCookieName = "hiwall"

var hiReactions = []struct {
	Key   string
	Emoji string
}{
	{"wave", "👋"},
	{"thumbs", "👍"},
	{"heart", "❤️"},
	{"fire", "🔥"},
	{"gmi", "gmi"},
	{"ngmi", "ngmi"},
}

// alreadySaidHi checks whether the user has said hi on this path and returns
// the reaction they used. Entries in the old format (no "|") are treated as "wave".
func alreadySaidHi(r *http.Request, path string) (bool, string) {
	c, err := r.Cookie(hiCookieName)
	if err != nil {
		return false, ""
	}
	for _, entry := range strings.Split(c.Value, ",") {
		if strings.Contains(entry, "|") {
			parts := strings.SplitN(entry, "|", 2)
			if parts[0] == path {
				return true, parts[1]
			}
		} else if entry == path {
			return true, "wave"
		}
	}
	return false, ""
}

func markSaidHi(w http.ResponseWriter, r *http.Request, path, reaction string) {
	existing := ""
	if c, err := r.Cookie(hiCookieName); err == nil {
		existing = c.Value
	}
	entry := path + "|" + reaction
	val := entry
	if existing != "" {
		val = existing + "," + entry
	}
	http.SetCookie(w, &http.Cookie{
		Name:     hiCookieName,
		Value:    val,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		SameSite: http.SameSiteLaxMode,
	})
}

func reactionEmoji(key string) string {
	for _, rx := range hiReactions {
		if rx.Key == key {
			return rx.Emoji
		}
	}
	return "👋"
}

// hiWidget renders the hi widget fragment.
func hiWidget(w http.ResponseWriter, path string, total int, reactions map[string]int, todayCount int, didHi bool, myReaction string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if didHi {
		// Post-click: waving back + reaction bar + today count + wall link.
		myEmoji := reactionEmoji(myReaction)

		reactionBar := ""
		for _, rx := range hiReactions {
			if c := reactions[rx.Key]; c > 0 {
				reactionBar += fmt.Sprintf(`<span class="hi-react-item">%s %d</span>`, rx.Emoji, c)
			}
		}

		todayStr := ""
		if todayCount > 0 {
			todayStr = fmt.Sprintf(`<span class="hi-today">&middot; +%d today</span>`, todayCount)
		}

		fmt.Fprintf(w,
			`<div id="hi-widget" class="hi-widget hi-done">
  <span class="hi-you"><span class="hi-wave-emoji">%s</span> hi back!</span>
  <span class="hi-divider">&middot;</span>
  <div class="hi-reaction-bar">%s</div>
  %s
  <span class="hi-total">%d total</span>
  <a href="/hi-wall" class="hi-wall-link">wall &rarr;</a>
</div>`, myEmoji, reactionBar, todayStr, total)
		return
	}

	// Pre-click: reaction picker + avatar stack + count + wall link.
	btns := ""
	for _, rx := range hiReactions {
		btns += fmt.Sprintf(
			`<button class="hi-reaction-btn" title="%s" hx-post="/api/hi" hx-target="#hi-widget" hx-swap="outerHTML" hx-vals="{&quot;path&quot;:&quot;%s&quot;,&quot;reaction&quot;:&quot;%s&quot;}">%s</button>`,
			rx.Key, path, rx.Key, rx.Emoji)
	}

	dots := total
	if dots > 3 {
		dots = 3
	}
	avatarHTML := `<div class="hi-avatar-stack">`
	for i := 0; i < dots; i++ {
		avatarHTML += `<div class="hi-dot"></div>`
	}
	avatarHTML += `</div>`

	countStr := "be the first"
	if total == 1 {
		countStr = "1 said hi"
	} else if total > 1 {
		countStr = fmt.Sprintf("%d said hi", total)
	}

	fmt.Fprintf(w,
		`<div id="hi-widget" class="hi-widget">
  <div class="hi-reactions-wrap">
    <span class="hi-prompt">say hi:</span>
    <div class="hi-reaction-btns">%s</div>
  </div>
  <div class="hi-meta">
    %s
    <span class="hi-count">%s</span>
  </div>
  <a href="/hi-wall" class="hi-wall-link">wall &rarr;</a>
</div>`, btns, avatarHTML, countStr)
}

// HiGet returns the current hi widget state for a page (loaded by HTMX on page init).
func (h *Handler) HiGet(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" || len(path) > 200 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	total, reactions, todayCount := h.db.HiGetAll(path)
	didHi, myReaction := alreadySaidHi(r, path)
	hiWidget(w, path, total, reactions, todayCount, didHi, myReaction)
}

// HiPost records a reaction for a page and returns the updated widget.
func (h *Handler) HiPost(w http.ResponseWriter, r *http.Request) {
	path := r.FormValue("path")
	reaction := r.FormValue("reaction")
	if path == "" || len(path) > 200 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Validate reaction against known values.
	valid := false
	for _, rx := range hiReactions {
		if rx.Key == reaction {
			valid = true
			break
		}
	}
	if !valid {
		reaction = "wave"
	}

	didHi, myReaction := alreadySaidHi(r, path)
	var total int
	var reactions map[string]int
	var todayCount int
	if !didHi {
		total, reactions, todayCount = h.db.HiIncrementReaction(path, reaction)
		myReaction = reaction
		markSaidHi(w, r, path, reaction)
	} else {
		total, reactions, todayCount = h.db.HiGetAll(path)
	}
	hiWidget(w, path, total, reactions, todayCount, true, myReaction)
}

type HiWallData struct {
	BaseData
	Pages  interface{}
	OGTitle string
	OGDesc  string
	OGUrl   string
}

// HiWall renders the hi wall page — top pages by hi count.
func (h *Handler) HiWall(w http.ResponseWriter, r *http.Request) {
	pages, err := h.db.HiTopWallPages(50)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Error", "Could not load hi wall")
		return
	}
	h.render(w, "hi_wall", HiWallData{BaseData: h.baseData(r), Pages: pages})
}

