package request_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/shapes"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// exampleNetflowFigures are netflow figures whose every field differs from every other.
func exampleNetflowFigures() reads.VolumeFigures {
	figures := reads.VolumeFigures{Bytes: 600, AllowedBytes: 300, BlockedBytes: 200, UnknownBytes: 100,
		AllowedConnections: 3, BlockedConnections: 2, UnknownConnections: 1, Netflow: true}
	for index, field := range figures.Parts.Fields() {
		*field = int64(index + 1)
	}
	return figures
}

// TestScriptFiguresCarryEveryNetflowPartMember is the members a script's netflow figures carry: the
// columns of netflow_parts by name, so a statement's (netflow_sum(...)).* row is read as figures, and
// figures handed to a script and back are the same figures.
func TestScriptFiguresCarryEveryNetflowPartMember(t *testing.T) {
	t.Parallel()
	var names []string
	members := reflect.TypeOf(traffic.NetflowMembers{})
	for index := 0; index < members.NumField(); index++ {
		tag := strings.Split(members.Field(index).Tag.Get("json"), ",")[0]
		if members.Field(index).Type == reflect.TypeOf((*int64)(nil)) {
			names = append(names, tag)
		}
	}
	if strings.Join(names, ",") != strings.Join(reads.NetflowPartsColumns(), ",") {
		t.Errorf("the script's netflow members are\n%v\nnot the columns of netflow_parts\n%v", names,
			reads.NetflowPartsColumns())
	}
	figures := exampleNetflowFigures()
	encoded, err := json.Marshal(traffic.Summable(figures))
	if err != nil {
		t.Fatal(err)
	}
	var read traffic.SummableFigures
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatal(err)
	}
	if read.ToStore() != figures {
		t.Errorf("figures handed to a script and back are\n%+v\nnot\n%+v", read.ToStore(), figures)
	}
	logged := figures
	logged.Netflow, logged.Parts = false, reads.NetflowParts{}
	if encoded, _ := json.Marshal(traffic.Summable(logged)); strings.Contains(string(encoded), "packets") {
		t.Errorf("logged figures carry netflow members: %s", encoded)
	}
}

// TestANetflowVolumeHandedBackKeepsItsParts is "coverage per action" through a script: a netflow
// Volume carries the packets and coverage of each part, so a script summing Volumes it was handed
// (owner_activity, client_volume_ranking) keeps every part's own figures.
func TestANetflowVolumeHandedBackKeepsItsParts(t *testing.T) {
	t.Parallel()
	figures := exampleNetflowFigures()
	volume := request.NetflowVolume(figures)
	if *volume.Packets != figures.Packets() || *volume.BlockedPackets != figures.Parts.Blocked.Packets ||
		volume.BlockedCoverage.StoreCoverage() != figures.Parts.Blocked.Coverage ||
		volume.Coverage.StoreCoverage() != figures.Coverage() {
		t.Errorf("the Volume is %+v", volume)
	}
	encoded, err := json.Marshal(volume)
	if err != nil {
		t.Fatal(err)
	}
	var read traffic.SummableFigures
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatal(err)
	}
	if read.ToStore() != figures {
		t.Errorf("a Volume handed back is read as\n%+v\nnot\n%+v", read.ToStore(), figures)
	}
	blocked := shapes.BlockedOnly(figures)
	if blocked.Parts != (reads.NetflowParts{Blocked: figures.Parts.Blocked}) || blocked.Bytes != figures.BlockedBytes ||
		blocked.Connections() != figures.BlockedConnections {
		t.Errorf("the blocked part is %+v", blocked)
	}
	allowed := shapes.AllowedOnly(figures)
	if allowed.Parts != (reads.NetflowParts{Allowed: figures.Parts.Allowed}) || allowed.Bytes != figures.AllowedBytes {
		t.Errorf("the allowed part is %+v", allowed)
	}
}
