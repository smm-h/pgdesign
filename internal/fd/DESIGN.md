# internal/fd

Functional dependency algorithms. Shared by validate/ and audit/.

## Functions

- `Closure(attrs []string, fds []FuncDep) []string` -- Attribute closure using Armstrong's axioms. Iterative fixed-point: start with attrs, repeatedly add B for any FD A->B where A is subset of current closure. Returns the full closure set.

- `MinimalCover(fds []FuncDep) []FuncDep` -- Compute minimal (canonical) cover. Steps: (1) decompose RHS to single attributes, (2) remove extraneous LHS attributes, (3) remove redundant FDs.

- `CandidateKeys(allAttrs []string, fds []FuncDep) [][]string` -- Find all minimal superkeys. Bottom-up search: start with single attributes, check if closure = allAttrs. Prune: if X is a superkey, no superset of X is minimal. Returns all candidate keys.

- `IsSuperkey(attrs []string, allAttrs []string, fds []FuncDep) bool` -- Check if closure of attrs covers all attributes. Convenience wrapper around Closure.

- `IsPrime(attr string, candidateKeys [][]string) bool` -- Check if an attribute appears in any candidate key.

## Complexity

- Closure: O(|fds| * |attrs|) per call
- MinimalCover: O(|fds|^2 * |attrs|)
- CandidateKeys: O(2^|attrs|) worst case, but pruning makes it practical for tables with <30 columns

## Usage

- `validate/` uses Closure to check if FK columns form a valid reference
- `audit/` uses CandidateKeys + IsPrime for 2NF/3NF violation detection
- `audit/` uses MinimalCover for Bernstein's synthesis (decomposition suggestions)
