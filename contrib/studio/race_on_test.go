//go:build race

package studio

// raceEnabled is true under the race detector, which makes a sort of 100,000
// rows dozens of times slower than the budget is meant for.
const raceEnabled = true
