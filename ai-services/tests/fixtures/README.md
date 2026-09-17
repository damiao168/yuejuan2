# Paper parser replay fixtures

`expected_multimodal_math_paper.json` is a manually reviewed expected result
for the sample mathematics page. It is not a captured DeepSeek response and
must not be presented as provider benchmark evidence.

The parser tests inject this JSON through a fake model adapter. This exercises
schema validation, source grounding, formula preservation, and noise filtering
without making a paid network request. A future real provider response may be
added only after its exact validated JSON has been captured and labelled with
the provider model/version.
