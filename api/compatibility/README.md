# Initial compatibility baseline

`baseline.binpb` is the first `marketdata.v1` contract, established in migration phase 1 before any client release. Its initial bytes match the generated descriptor, but it is a separate checked-in file and generation never updates it.

Run Buf FILE rules against this baseline. FILE checks both wire and generated source compatibility, including field types/numbers, method removal, presence and reserved fields. `scripts/api/test_generation.py` uses isolated mutated descriptor fixtures to prove accepted optional additions and rejected breaking changes.

Removed fields must reserve both their number and name. A future baseline change requires a written reason and compatibility review. Do not regenerate it to make a failure disappear.
