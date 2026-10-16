package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// The Star Wars schema from the GraphQL specification and graphql-js.

const starWarsSDL = `
"One of the films in the Star Wars Trilogy"
enum Episode {
  "Released in 1977."
  NEWHOPE
  "Released in 1980."
  EMPIRE
  "Released in 1983."
  JEDI
}

"A character in the Star Wars Trilogy"
interface Character {
  "The id of the character."
  id: ID!
  "The name of the character."
  name: String
  "The friends of the character, or an empty list if they have none."
  friends: [Character]
  "Which movies they appear in."
  appearsIn: [Episode]
  "All secrets about their past."
  secretBackstory: String
}

"A humanoid creature in the Star Wars universe."
type Human implements Character {
  id: ID!
  name: String
  friends: [Character]
  appearsIn: [Episode]
  "The home planet of the human, or null if unknown."
  homePlanet: String
  secretBackstory: String
}

"A mechanical creature in the Star Wars universe."
type Droid implements Character {
  id: ID!
  name: String
  friends: [Character]
  appearsIn: [Episode]
  secretBackstory: String
  "The primary function of the droid."
  primaryFunction: String
}

type Query {
  hero("If omitted, returns the hero of the whole saga." episode: Episode): Character
  human("id of the human" id: ID!): Human
  droid("id of the droid" id: ID!): Droid
}
`

type character struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	FriendIDs       []string `json:"-"`
	AppearsIn       []string `json:"appearsIn"`
	HomePlanet      *string  `json:"homePlanet"`
	PrimaryFunction string   `json:"primaryFunction"`
	droid           bool
}

func (c *character) GraphQLTypename() string {
	if c.droid {
		return "Droid"
	}

	return "Human"
}

func starWarsData() map[string]*character {
	all := []*character{
		{
			ID:         "1000",
			Name:       "Luke Skywalker",
			FriendIDs:  []string{"1002", "1003", "2000", "2001"},
			AppearsIn:  []string{"NEWHOPE", "EMPIRE", "JEDI"},
			HomePlanet: new("Tatooine"),
		},
		{
			ID:         "1001",
			Name:       "Darth Vader",
			FriendIDs:  []string{"1004"},
			AppearsIn:  []string{"NEWHOPE", "EMPIRE", "JEDI"},
			HomePlanet: new("Tatooine"),
		},
		{
			ID:        "1002",
			Name:      "Han Solo",
			FriendIDs: []string{"1000", "1003", "2001"},
			AppearsIn: []string{"NEWHOPE", "EMPIRE", "JEDI"},
		},
		{
			ID:         "1003",
			Name:       "Leia Organa",
			FriendIDs:  []string{"1000", "1002", "2000", "2001"},
			AppearsIn:  []string{"NEWHOPE", "EMPIRE", "JEDI"},
			HomePlanet: new("Alderaan"),
		},
		{ID: "1004", Name: "Wilhuff Tarkin", FriendIDs: []string{"1001"}, AppearsIn: []string{"NEWHOPE"}},
		{
			ID:              "2000",
			Name:            "C-3PO",
			FriendIDs:       []string{"1000", "1002", "1003", "2001"},
			AppearsIn:       []string{"NEWHOPE", "EMPIRE", "JEDI"},
			PrimaryFunction: "Protocol",
			droid:           true,
		},
		{
			ID:              "2001",
			Name:            "R2-D2",
			FriendIDs:       []string{"1000", "1002", "1003"},
			AppearsIn:       []string{"NEWHOPE", "EMPIRE", "JEDI"},
			PrimaryFunction: "Astromech",
			droid:           true,
		},
	}

	m := map[string]*character{}
	for _, c := range all {
		m[c.ID] = c
	}

	return m
}

type idArgs struct {
	ID string `json:"id"`
}

func starWarsSchema(t *testing.T, opts ...Option) *Schema {
	t.Helper()

	data := starWarsData()
	friends := func(_ context.Context, c *character, _ NoArgs) ([]*character, error) {
		out := make([]*character, 0, len(c.FriendIDs))
		for _, id := range c.FriendIDs {
			out = append(out, data[id])
		}

		return out, nil
	}
	backstory := func(context.Context, *character, NoArgs) (*string, error) {
		return nil, errors.New("secretBackstory is secret")
	}

	base := []Option{
		Query("hero", func(_ context.Context, a struct {
			Episode *string `json:"episode"`
		},
		) (*character, error) {
			if a.Episode != nil && *a.Episode == "EMPIRE" {
				return data["1000"], nil
			}

			return data["2001"], nil
		}),
		Query("human", func(_ context.Context, a idArgs) (*character, error) {
			if c := data[a.ID]; c != nil && !c.droid {
				return c, nil
			}

			return nil, nil
		}),
		Query("droid", func(_ context.Context, a idArgs) (*character, error) {
			if c := data[a.ID]; c != nil && c.droid {
				return c, nil
			}

			return nil, nil
		}),
		Field("Human", "friends", friends),
		Field("Droid", "friends", friends),
		Field("Human", "secretBackstory", backstory),
		Field("Droid", "secretBackstory", backstory),
	}

	s, err := New(starWarsSDL, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New(starWars) error = %v", err)
	}

	return s
}

func run(t *testing.T, s *Schema, query string, vars map[string]any) string {
	t.Helper()

	b, err := json.Marshal(s.Execute(t.Context(), Request{Query: query, Variables: vars}))
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func jsonEqual(t *testing.T, want, got string) {
	t.Helper()

	var w, g any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want JSON %q: %v", want, err)
	}

	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("bad got JSON %q: %v", got, err)
	}

	if diff := cmp.Diff(w, g); diff != "" {
		t.Errorf("response (-want +got):\n%s\ngot: %s", diff, got)
	}
}

func TestStarWarsQueries(t *testing.T) {
	t.Parallel()

	s := starWarsSchema(t)

	tests := []struct {
		name, query string
		vars        map[string]any
		want        string
	}{
		{"hero name", `query HeroNameQuery { hero { name } }`, nil, `{"data":{"hero":{"name":"R2-D2"}}}`},
		{
			"id and friends",
			`{ hero { id name friends { name } } }`,
			nil,
			`{"data":{"hero":{"id":"2001","name":"R2-D2","friends":[{"name":"Luke Skywalker"},{"name":"Han Solo"},{"name":"Leia Organa"}]}}}`,
		},
		{
			"nested friends", `{ hero { name friends { name appearsIn friends { name } } } }`, nil,
			`{"data":{"hero":{"name":"R2-D2","friends":[
				{"name":"Luke Skywalker","appearsIn":["NEWHOPE","EMPIRE","JEDI"],"friends":[{"name":"Han Solo"},{"name":"Leia Organa"},{"name":"C-3PO"},{"name":"R2-D2"}]},
				{"name":"Han Solo","appearsIn":["NEWHOPE","EMPIRE","JEDI"],"friends":[{"name":"Luke Skywalker"},{"name":"Leia Organa"},{"name":"R2-D2"}]},
				{"name":"Leia Organa","appearsIn":["NEWHOPE","EMPIRE","JEDI"],"friends":[{"name":"Luke Skywalker"},{"name":"Han Solo"},{"name":"C-3PO"},{"name":"R2-D2"}]}]}}}`,
		},
		{"argument", `{ human(id: "1000") { name } }`, nil, `{"data":{"human":{"name":"Luke Skywalker"}}}`},
		{
			"variable",
			`query F($someId: ID!) { human(id: $someId) { name } }`,
			map[string]any{"someId": "1002"},
			`{"data":{"human":{"name":"Han Solo"}}}`,
		},
		{
			"integer id variable",
			`query F($someId: ID!) { human(id: $someId) { name } }`,
			map[string]any{"someId": json.Number("1003")},
			`{"data":{"human":{"name":"Leia Organa"}}}`,
		},
		{
			"not found is null",
			`query F($id: ID!) { human(id: $id) { name } }`,
			map[string]any{"id": "not a valid id"},
			`{"data":{"human":null}}`,
		},
		{"alias", `{ luke: human(id: "1000") { name } }`, nil, `{"data":{"luke":{"name":"Luke Skywalker"}}}`},
		{
			"two aliases", `{ luke: human(id: "1000") { name } leia: human(id: "1003") { name } }`, nil,
			`{"data":{"luke":{"name":"Luke Skywalker"},"leia":{"name":"Leia Organa"}}}`,
		},
		{
			"fragments",
			`{ luke: human(id: "1000") { ...HumanFragment } leia: human(id: "1003") { ...HumanFragment } }
			fragment HumanFragment on Human { name homePlanet }`,
			nil,
			`{"data":{"luke":{"name":"Luke Skywalker","homePlanet":"Tatooine"},"leia":{"name":"Leia Organa","homePlanet":"Alderaan"}}}`,
		},
		{"typename", `{ hero { __typename name } }`, nil, `{"data":{"hero":{"__typename":"Droid","name":"R2-D2"}}}`},
		{
			"enum argument",
			`{ hero(episode: EMPIRE) { __typename name } }`,
			nil,
			`{"data":{"hero":{"__typename":"Human","name":"Luke Skywalker"}}}`,
		},
		{
			"inline fragments", `{ hero { name ... on Droid { primaryFunction } ... on Human { homePlanet } } }`, nil,
			`{"data":{"hero":{"name":"R2-D2","primaryFunction":"Astromech"}}}`,
		},
		{
			"error with path",
			`{ hero { name secretBackstory } }`,
			nil,
			`{"errors":[{"message":"Internal server error","locations":[{"line":1,"column":15}],"path":["hero","secretBackstory"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}],
			  "data":{"hero":{"name":"R2-D2","secretBackstory":null}}}`,
		},
		{
			"errors in list", `{ hero { name friends { name secretBackstory } } }`, nil,
			`{"errors":[
				{"message":"Internal server error","locations":[{"line":1,"column":30}],"path":["hero","friends",0,"secretBackstory"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}},
				{"message":"Internal server error","locations":[{"line":1,"column":30}],"path":["hero","friends",1,"secretBackstory"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}},
				{"message":"Internal server error","locations":[{"line":1,"column":30}],"path":["hero","friends",2,"secretBackstory"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}],
			  "data":{"hero":{"name":"R2-D2","friends":[{"name":"Luke Skywalker","secretBackstory":null},{"name":"Han Solo","secretBackstory":null},{"name":"Leia Organa","secretBackstory":null}]}}}`,
		},
		{
			"skip and include",
			`query ($no: Boolean!) { hero { name @skip(if: true) id @include(if: $no) ... @include(if: true) { __typename } } }`,
			map[string]any{"no": false},
			`{"data":{"hero":{"__typename":"Droid"}}}`,
		},
		{
			"merged fields", `{ hero { name } hero { id } }`, nil,
			`{"data":{"hero":{"name":"R2-D2","id":"2001"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			jsonEqual(t, tt.want, run(t, s, tt.query, tt.vars))
		})
	}
}

func TestStarWarsDebugShowsInternalErrors(t *testing.T) {
	t.Parallel()

	s := starWarsSchema(t, WithDebug(true))
	got := run(t, s, `{ hero { secretBackstory } }`, nil)
	jsonEqual(t, `{"errors":[{"message":"secretBackstory is secret","locations":[{"line":1,"column":10}],
		"path":["hero","secretBackstory"],"extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":{"hero":{"secretBackstory":null}}}`, got)
}
