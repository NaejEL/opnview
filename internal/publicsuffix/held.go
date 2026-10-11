package publicsuffix

// Held reports the copy in use, from this process's memory: whether there is one,
// its own VERSION line, how many rules it holds, and the last instant a refresh
// confirmed it current -- a download, or a 304 answered for it -- with 0 when no
// refresh has.
//
// IT READS NOTHING AND CONTACTS NOTHING. It is what the settings surface draws the
// list's figures from, so that they come from the copy actually in use rather than
// from the free text of the availability row, which is a description for a person
// and not a value to parse.
func (r *Refresher) Held() (version string, rules int, confirmedAt int64, held bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	if r.list == nil {
		return "", 0, r.meta.RefreshedAt, false
	}
	return r.list.Version, r.list.Rules(), r.meta.RefreshedAt, true
}
