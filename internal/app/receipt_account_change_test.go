package app

import (
	"bytes"
	"testing"
)

func TestAccountChangePreservesReceipts(t *testing.T) {
	for _, tc := range []struct {
		name, kind, nextKind       string
		from, to, nextFrom, nextTo int
		receiptAccounts            map[int]int
	}{
		{"expense", "expense", "expense", 0, -1, 2, -1, map[int]int{0: 2}},
		{"income", "income", "income", 0, -1, 2, -1, map[int]int{0: 2}},
		{"transfer source", "transfer", "transfer", 0, 1, 2, 1, map[int]int{0: 2, 1: 1}},
		{"transfer target", "transfer", "transfer", 0, 1, 0, 2, map[int]int{0: 0, 1: 2}},
		{"both transfer accounts", "transfer", "transfer", 0, 1, 2, 3, map[int]int{0: 2, 1: 3}},
		{"swap transfer accounts", "transfer", "transfer", 0, 1, 1, 0, map[int]int{0: 0, 1: 1}},
		{"transfer to expense", "transfer", "expense", 0, 1, 2, -1, map[int]int{0: 2, 1: 2}},
		{"income to transfer", "income", "transfer", 0, -1, 2, 3, map[int]int{0: 2}},
		{"expense to transfer", "expense", "transfer", 0, -1, 2, 3, map[int]int{0: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, c := newTestApp(t)
			ledger := c.personalLedger()
			accounts := []string{
				c.account(ledger, "A", "bank", "KRW", "1000"),
				c.account(ledger, "B", "bank", "KRW", "1000"),
				c.account(ledger, "C", "bank", "KRW", "1000"),
				c.account(ledger, "D", "bank", "KRW", "1000"),
			}
			input := txInput(accounts[tc.from], tc.kind, "100")
			if tc.to >= 0 {
				input.ToID = accounts[tc.to]
			}
			pid := c.create(input)
			var event string
			if err := a.DB.QueryRow("SELECT event_id FROM postings WHERE id=?", pid).Scan(&event); err != nil {
				t.Fatal(err)
			}
			original, err := eventPostings(a.DB, event)
			if err != nil {
				t.Fatal(err)
			}
			refs := map[int]ReceiptRef{}
			images := map[int][]byte{}
			for old := range tc.receiptAccounts {
				ref, _ := uploadPurgePhoto(t, c, original[accounts[old]])
				refs[old] = ref
				images[old] = c.send("GET", "/receipts/"+ref.ID, nil, 200, nil)
			}
			detached, _ := uploadPurgePhoto(t, c, pid)
			c.send("DELETE", "/receipts/"+detached.ID, nil, 200, nil)
			// The same image is deduplicated while attached; upload it again after detaching.
			active, _ := uploadPurgePhoto(t, c, pid)
			refs[tc.from] = active
			images[tc.from] = c.send("GET", "/receipts/"+active.ID, nil, 200, nil)

			input = txInput(accounts[tc.nextFrom], tc.nextKind, "200")
			input.Version = 1
			if tc.nextTo >= 0 {
				input.ToID = accounts[tc.nextTo]
			}
			var result struct {
				ID         string            `json:"id"`
				PostingIDs map[string]string `json:"posting_ids"`
			}
			c.send("PUT", "/transactions/"+pid, input, 200, &result)
			for old, target := range tc.receiptAccounts {
				ref := refs[old]
				var posting string
				if err := a.DB.QueryRow("SELECT posting_id FROM receipts WHERE id=?", ref.ID).Scan(&posting); err != nil {
					t.Fatal(err)
				}
				if posting != result.PostingIDs[accounts[target]] {
					t.Fatalf("receipt from %d moved to wrong leg", old)
				}
				if got := c.send("GET", "/receipts/"+ref.ID, nil, 200, nil); !bytes.Equal(got, images[old]) {
					t.Fatal("receipt image changed")
				}
			}
			c.send("GET", "/receipts/"+detached.ID, nil, 404, nil)
			for i, account := range accounts {
				want := "1000"
				if i == tc.nextFrom {
					if tc.nextKind == "income" {
						want = "1200"
					} else {
						want = "800"
					}
				}
				if i == tc.nextTo {
					want = "1200"
				}
				if got := c.balance(account); got != want {
					t.Fatalf("account %d balance %s, want %s", i, got, want)
				}
			}
			found := false
			for _, transaction := range c.report("").Items {
				for _, leg := range transaction.Legs {
					if leg.ID == result.ID {
						found = true
						if transaction.Version != 2 {
							t.Fatal("version not incremented")
						}
					}
				}
			}
			if !found {
				t.Fatal("updated entry missing from report")
			}
		})
	}
}

func TestMovedReceiptFollowsDestinationAccess(t *testing.T) {
	_, _, c := newTestApp(t)
	ledger := c.personalLedger()
	source := c.account(ledger, "Original", "bank", "KRW", "1000")
	target := c.account(ledger, "Corrected", "bank", "KRW", "1000")
	inv, token := c.invite("account", source, "editor")
	other := c.joinUser("receiptguest", token)
	c.send("POST", "/invitations/"+inv+"/approve", map[string]any{}, 200, nil)
	pid := c.create(txInput(source, "expense", "100"))
	ref, _ := uploadPurgePhoto(t, c, pid)
	other.send("GET", "/receipts/"+ref.ID, nil, 200, nil)
	input := txInput(target, "expense", "100")
	input.Version = 1
	other.send("PUT", "/transactions/"+pid, input, 404, nil)
	other.send("GET", "/receipts/"+ref.ID, nil, 200, nil)
	c.send("PUT", "/transactions/"+pid, input, 200, nil)
	other.send("GET", "/receipts/"+ref.ID, nil, 404, nil)
	c.send("GET", "/receipts/"+ref.ID, nil, 200, nil)
	inv, token = c.invite("account", target, "viewer")
	reader := c.joinUser("receiptreader", token)
	c.send("POST", "/invitations/"+inv+"/approve", map[string]any{}, 200, nil)
	reader.send("GET", "/receipts/"+ref.ID, nil, 200, nil)
}
