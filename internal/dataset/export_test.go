package dataset

// RefusalCodes is every refusal code, which a test compares with the document.
func RefusalCodes() []string {
	return []string{RefusalUnknownDataset, RefusalMalformedParameters, RefusalUnknownColumn,
		RefusalColumnRoleNotAdmitted, RefusalAggregationNotAdmitted, RefusalOperatorNotAdmitted,
		RefusalValueTypeMismatch, RefusalBucketNotAdmitted, RefusalTooManyBuckets, RefusalTooManySeries,
		RefusalTooManyRows}
}
