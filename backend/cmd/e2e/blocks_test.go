//go:build e2e

package e2e

import "testing"

// A player's blocked list and an admin's view of blocks (docs/FRIENDS.md,
// "The blocked list"; docs/CONSOLE.md, "A player's blocks"): A blocks B and
// C, C blocks A; A's list names B and C newest first; the console sees both
// ways for A, lifts A's block of B and C's block of A, and refuses nobody.
func TestBlockedLists(t *testing.T) {
	a, pa := player(t, "Blocker")
	_, pb := player(t, "Blocked One")
	c, pc := player(t, "Blocked Two")
	aID, bID, cID := pa["id"].(string), pb["id"].(string), pc["id"].(string)

	a.call("POST", "/v1/players/"+bID+"/block", nil, 204, nil)
	a.call("POST", "/v1/players/"+cID+"/block", nil, 204, nil)
	c.call("POST", "/v1/players/"+aID+"/block", nil, 204, nil)

	type shown struct {
		ID        string `json:"id"`
		Nickname  string `json:"nickname"`
		Handle    string `json:"handle"`
		BlockedAt string `json:"blocked_at"`
	}
	var mine struct {
		Blocked []string `json:"blocked"`
		Players []shown  `json:"players"`
	}
	a.call("GET", "/v1/friends/blocked", nil, 200, &mine)
	if len(mine.Blocked) != 2 || len(mine.Players) != 2 {
		t.Fatalf("A's blocked list: %+v", mine)
	}
	if mine.Players[0].ID != cID || mine.Players[1].ID != bID || mine.Players[0].BlockedAt == "" ||
		mine.Players[0].Nickname != "Blocked Two" || mine.Players[0].Handle != pc["handle"] {
		t.Fatalf("want C then B, each with its card and time: %+v", mine.Players)
	}
	t.Logf("ok   A's blocked list names C then B, newest first, with the ids the chat reads")

	admin := newClient(t)
	admin.origin = console
	admin.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, nil)

	type row struct {
		ID        string `json:"id"`
		Disabled  bool   `json:"disabled"`
		BlockedAt string `json:"blocked_at"`
	}
	var blocks struct {
		Blocked   []row `json:"blocked"`
		BlockedBy []row `json:"blocked_by"`
	}
	admin.call("GET", "/v1/admin/players/"+aID+"/blocks", nil, 200, &blocks)
	if len(blocks.Blocked) != 2 || len(blocks.BlockedBy) != 1 || blocks.BlockedBy[0].ID != cID ||
		blocks.Blocked[0].BlockedAt == "" {
		t.Fatalf("A's blocks in the console: %+v", blocks)
	}
	t.Logf("ok   the console reads A's blocks both ways: B and C blocked, C blocking")

	admin.call("DELETE", "/v1/admin/players/"+aID+"/blocks/"+bID, nil, 204, nil)
	admin.call("DELETE", "/v1/admin/players/"+cID+"/blocks/"+aID, nil, 204, nil)
	admin.call("DELETE", "/v1/admin/players/"+aID+"/blocks/"+bID, nil, 204, nil)
	admin.call("GET", "/v1/admin/players/"+aID+"/blocks", nil, 200, &blocks)
	if len(blocks.Blocked) != 1 || blocks.Blocked[0].ID != cID || len(blocks.BlockedBy) != 0 {
		t.Fatalf("after lifting two: %+v", blocks)
	}
	a.call("GET", "/v1/friends/blocked", nil, 200, &mine)
	if len(mine.Players) != 1 || mine.Players[0].ID != cID {
		t.Fatalf("A's list after the console lifted B: %+v", mine)
	}
	t.Logf("ok   the console lifted A's block of B and C's block of A; lifting again is 204")

	var refused errorJSON
	admin.call("GET", "/v1/admin/players/ply_nobodyxxxxxxxxxx/blocks", nil, 404, &refused)
	if refused.Error != "not_found" {
		t.Fatalf("the blocks of nobody: %+v", refused)
	}
	admin.call("DELETE", "/v1/admin/players/ply_nobodyxxxxxxxxxx/blocks/"+bID, nil, 404, &refused)
	if refused.Error != "not_found" {
		t.Fatalf("lifting for nobody: %+v", refused)
	}
	t.Logf("ok   nobody's blocks and lifting for nobody are not_found")

	// Clean up what this sweep made.
	a.call("DELETE", "/v1/players/"+cID+"/block", nil, 204, nil)
}
