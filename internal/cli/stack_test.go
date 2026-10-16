package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestChooseStack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interactive bool
		input       string
		api         bool
		inertia     string
		want        stack
		wantErr     string
		wantPrompt  bool
	}{
		{name: "no tty defaults to htmx", want: stack{kind: "htmx"}},
		{name: "api flag", api: true, want: stack{kind: "api"}},
		{name: "inertia flag", inertia: "vue", want: stack{kind: "inertia", framework: "vue"}},
		{
			name:    "inertia flag is case-insensitive",
			inertia: "Svelte",
			want:    stack{kind: "inertia", framework: "svelte"},
		},
		{name: "bad inertia flag", inertia: "angular", wantErr: "react, vue or svelte"},
		{name: "api and inertia", api: true, inertia: "react", wantErr: "not both"},
		{name: "flags skip the prompt", interactive: true, api: true, want: stack{kind: "api"}},
		{name: "prompt default", interactive: true, input: "\n", want: stack{kind: "htmx"}, wantPrompt: true},
		{name: "prompt api", interactive: true, input: "3\n", want: stack{kind: "api"}, wantPrompt: true},
		{
			name: "prompt inertia default react", interactive: true, input: "2\r\n\r\n",
			want: stack{kind: "inertia", framework: "react"}, wantPrompt: true,
		},
		{
			name: "prompt inertia svelte by name", interactive: true, input: "inertia\nsvelte\n",
			want: stack{kind: "inertia", framework: "svelte"}, wantPrompt: true,
		},
		{
			name:        "prompt retries bad answers",
			interactive: true,
			input:       "9\nhtmx\n",
			want:        stack{kind: "htmx"},
			wantPrompt:  true,
		},
		{
			name:        "prompt at EOF uses the default",
			interactive: true,
			input:       "",
			want:        stack{kind: "htmx"},
			wantPrompt:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			env := &Env{Stdin: strings.NewReader(tt.input), Stdout: &out, Interactive: tt.interactive}

			got, err := chooseStack(env, tt.api, tt.inertia)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("chooseStack() error = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if err != nil || got != tt.want {
				t.Fatalf("chooseStack() = %+v, %v, want %+v", got, err, tt.want)
			}

			if prompted := strings.Contains(out.String(), "Which stack"); prompted != tt.wantPrompt {
				t.Errorf("prompted = %v, want %v (output %q)", prompted, tt.wantPrompt, out.String())
			}
		})
	}
}
