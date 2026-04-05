package bmapi

type Player struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type BMResponse struct {
	Data     BMData       `json:"data"`
	Included []BMIncluded `json:"included"`
}

type BMData struct {
	ID            string          `json:"id"`
	Relationships BMRelationships `json:"relationships"`
}

type BMRelationships struct {
	Players BMPlayers `json:"players"`
}

type BMPlayers struct {
	Data []BMPlayerData `json:"data"`
}

type BMPlayerData struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type BMIncluded struct {
	Type       string       `json:"type"` // "player"
	ID         string       `json:"id"`
	Attributes BMAttributes `json:"attributes"`
}

type BMAttributes struct {
	Name string `json:"name"`
}
