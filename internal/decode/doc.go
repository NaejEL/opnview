// Package decode reads what an OPNsense endpoint actually said, and turns it into the shapes
// the schema stores. Two things, and nothing else: the tolerant field readers over a decoded
// JSON response, and the normalisation of the three timestamp shapes into the UTC epoch every
// column holds.
//
// WHY IT IS ITS OWN PACKAGE, and it is not for a directory layout. These functions encode
// MEASURED facts about the firewall's API, and they are the layer that enforces the rule the
// whole product rests on — a field that is absent, or present in a shape that cannot be read,
// is reported as ABSENT, so that nothing downstream ever writes a zero that means "we could not
// look". Inside the collector they were exercised only incidentally, through whichever
// implementation happened to meet a shape; here they are tested directly against the shapes
// docs/opnsense-api-survey.md records, and the tests can name the measurement each case comes
// from.
//
// WHY THE NAME IS `decode`. OPNsense has no word for this — it has no such layer, which is the
// reason this one exists — so the term is opnview's own and is recorded here as CLAUDE.md
// requires. `decode` was chosen over a collector-flavoured name because both sides of the
// seam this layer serves need it: when connectors become separate processes, a connector
// decodes its own product's answers and normalises its own timestamps, and a package named for
// the host would then be named for the wrong half. It reads a source's answer; it knows nothing
// about kinds, providers, the store, or what any of it is for.
//
// WHAT IS NOT HERE, because it was here and did not belong: folding several failures into one
// belongs to a pass that reads five kinds, and truncating an epoch to its UTC day is arithmetic
// on a value opnview already owns rather than on anything a source said. Both stayed in
// internal/collect, each with a line saying why.
package decode
