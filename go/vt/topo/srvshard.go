// Copyright 2012, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package topo

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/youtube/vitess/go/bson"
	"github.com/youtube/vitess/go/bytes2"
	"github.com/youtube/vitess/go/vt/key"
)

// This is the shard name for when the keyrange covers the entire space
// for unsharded database.
const SHARD_ZERO = "0"

// SrvShard contains a roll-up of the shard in the local namespace.
// In zk, it is under /zk/<cell>/vt/ns/<keyspace>/<shard>
type SrvShard struct {
	// Copied from Shard
	KeyRange    key.KeyRange
	ServedTypes []TabletType

	// TabletTypes represents the list of types we have serving tablets
	// for, in this cell only.
	TabletTypes []TabletType

	// For atomic updates
	version int64
}

type SrvShardArray []SrvShard

func (sa SrvShardArray) Len() int { return len(sa) }

func (sa SrvShardArray) Less(i, j int) bool {
	return sa[i].KeyRange.Start < sa[j].KeyRange.Start
}

func (sa SrvShardArray) Swap(i, j int) {
	sa[i], sa[j] = sa[j], sa[i]
}

func (sa SrvShardArray) Sort() { sort.Sort(sa) }

func NewSrvShard(version int64) *SrvShard {
	return &SrvShard{
		version: version,
	}
}

func EncodeTabletTypeArray(buf *bytes2.ChunkedWriter, name string, values []TabletType) {
	if len(values) == 0 {
		bson.EncodePrefix(buf, bson.Null, name)
	} else {
		bson.EncodePrefix(buf, bson.Array, name)
		lenWriter := bson.NewLenWriter(buf)
		for i, val := range values {
			bson.EncodeString(buf, bson.Itoa(i), string(val))
		}
		buf.WriteByte(0)
		lenWriter.RecordLen()
	}
}

func DecodeTabletTypeArray(buf *bytes.Buffer, kind byte) []TabletType {
	switch kind {
	case bson.Array:
		// valid
	case bson.Null:
		return nil
	default:
		panic(bson.NewBsonError("Unexpected data type %v for TabletType array", kind))
	}

	bson.Next(buf, 4)
	values := make([]TabletType, 0, 8)
	kind = bson.NextByte(buf)
	for kind != bson.EOO {
		if kind != bson.Binary {
			panic(bson.NewBsonError("Unexpected data type %v for TabletType array", kind))
		}
		bson.SkipIndex(buf)
		values = append(values, TabletType(bson.DecodeString(buf, kind)))
		kind = bson.NextByte(buf)
	}
	return values
}

func (ss *SrvShard) MarshalBson(buf *bytes2.ChunkedWriter, key string) {
	bson.EncodeOptionalPrefix(buf, bson.Object, key)
	lenWriter := bson.NewLenWriter(buf)

	ss.KeyRange.MarshalBson(buf, "KeyRange")
	EncodeTabletTypeArray(buf, "ServedTypes", ss.ServedTypes)
	EncodeTabletTypeArray(buf, "TabletTypes", ss.TabletTypes)

	buf.WriteByte(0)
	lenWriter.RecordLen()

}

func (ss *SrvShard) UnmarshalBson(buf *bytes.Buffer, kind byte) {
	bson.VerifyObject(kind)
	bson.Next(buf, 4)

	kind = bson.NextByte(buf)
	for kind != bson.EOO {
		keyName := bson.ReadCString(buf)
		switch keyName {
		case "KeyRange":
			ss.KeyRange.UnmarshalBson(buf, kind)
		case "ServedTypes":
			ss.ServedTypes = DecodeTabletTypeArray(buf, kind)
		case "TabletTypes":
			ss.TabletTypes = DecodeTabletTypeArray(buf, kind)
		default:
			bson.Skip(buf, kind)
		}
		kind = bson.NextByte(buf)
	}
}

func (ss *SrvShard) ShardName() string {
	if !ss.KeyRange.IsPartial() {
		return SHARD_ZERO
	}
	return fmt.Sprintf("%v-%v", string(ss.KeyRange.Start.Hex()), string(ss.KeyRange.End.Hex()))
}

// KeyspacePartition represents a continuous set of shards to
// serve an entire data set.
type KeyspacePartition struct {
	// List of non-overlapping continuous shards sorted by range.
	Shards []SrvShard
}

func EncodeSrvShardArray(buf *bytes2.ChunkedWriter, name string, values []SrvShard) {
	if len(values) == 0 {
		bson.EncodePrefix(buf, bson.Null, name)
	} else {
		bson.EncodePrefix(buf, bson.Array, name)
		lenWriter := bson.NewLenWriter(buf)
		for i, val := range values {
			val.MarshalBson(buf, bson.Itoa(i))
		}
		buf.WriteByte(0)
		lenWriter.RecordLen()
	}
}

func DecodeSrvShardArray(buf *bytes.Buffer, kind byte) []SrvShard {
	switch kind {
	case bson.Array:
		// valid
	case bson.Null:
		return nil
	default:
		panic(bson.NewBsonError("Unexpected data type %v for SrvShard array", kind))
	}

	bson.Next(buf, 4)
	values := make([]SrvShard, 0, 8)
	kind = bson.NextByte(buf)
	for kind != bson.EOO {
		if kind != bson.Object {
			panic(bson.NewBsonError("Unexpected data type %v for SrvShard array", kind))
		}
		bson.SkipIndex(buf)
		value := &SrvShard{}
		value.UnmarshalBson(buf, kind)
		values = append(values, *value)
		kind = bson.NextByte(buf)
	}
	return values
}

func (kp *KeyspacePartition) MarshalBson(buf *bytes2.ChunkedWriter, key string) {
	bson.EncodeOptionalPrefix(buf, bson.Object, key)
	lenWriter := bson.NewLenWriter(buf)

	EncodeSrvShardArray(buf, "Shards", kp.Shards)

	buf.WriteByte(0)
	lenWriter.RecordLen()
}

func (kp *KeyspacePartition) UnmarshalBson(buf *bytes.Buffer, kind byte) {
	bson.VerifyObject(kind)
	bson.Next(buf, 4)

	kind = bson.NextByte(buf)
	for kind != bson.EOO {
		keyName := bson.ReadCString(buf)
		switch keyName {
		case "Shards":
			kp.Shards = DecodeSrvShardArray(buf, kind)
		default:
			bson.Skip(buf, kind)
		}
		kind = bson.NextByte(buf)
	}
}

// A distilled serving copy of keyspace detail stored in the local
// cell for fast access. Derived from the global keyspace, shards and
// local details.
// In zk, it is in /zk/local/vt/ns/<keyspace>
type SrvKeyspace struct {
	// Shards to use per type, only contains complete partitions.
	Partitions map[TabletType]*KeyspacePartition

	// This list will be deprecated as soon as Partitions is used.
	// List of non-overlapping shards sorted by range.
	Shards []SrvShard

	// List of available tablet types for this keyspace in this cell.
	// May not have a server for every shard, but we have some.
	TabletTypes []TabletType

	// Copied from Keyspace
	ShardingColumnName string
	ShardingColumnType key.KeyspaceIdType
	ServedFrom         map[TabletType]string

	// For atomic updates
	version int64
}

func NewSrvKeyspace(version int64) *SrvKeyspace {
	return &SrvKeyspace{
		version: version,
	}
}

func EncodeKeyspacePartitionMap(buf *bytes2.ChunkedWriter, name string, values map[TabletType]*KeyspacePartition) {
	if len(values) == 0 {
		bson.EncodePrefix(buf, bson.Null, name)
	} else {
		bson.EncodePrefix(buf, bson.Object, name)
		lenWriter := bson.NewLenWriter(buf)
		for i, val := range values {
			val.MarshalBson(buf, string(i))
		}
		buf.WriteByte(0)
		lenWriter.RecordLen()
	}
}

func DecodeKeyspacePartitionMap(buf *bytes.Buffer, kind byte) map[TabletType]*KeyspacePartition {
	switch kind {
	case bson.Object:
		// valid
	case bson.Null:
		return nil
	default:
		panic(bson.NewBsonError("Unexpected data type %v for KeyspacePartition map", kind))
	}

	bson.Next(buf, 4)
	values := make(map[TabletType]*KeyspacePartition)
	kind = bson.NextByte(buf)
	for kind != bson.EOO {
		if kind != bson.Object {
			panic(bson.NewBsonError("Unexpected data type %v for KeyspacePartition map", kind))
		}
		keyName := bson.ReadCString(buf)
		value := &KeyspacePartition{}
		value.UnmarshalBson(buf, kind)
		values[TabletType(keyName)] = value
		kind = bson.NextByte(buf)
	}
	return values
}

func EncodeServedFrom(buf *bytes2.ChunkedWriter, name string, servedFrom map[TabletType]string) {
	bson.EncodePrefix(buf, bson.Object, name)
	lenWriter := bson.NewLenWriter(buf)
	for k, v := range servedFrom {
		bson.EncodeString(buf, string(k), v)
	}
	buf.WriteByte(0)
	lenWriter.RecordLen()
}

func DecodeServedFrom(buf *bytes.Buffer, kind byte) map[TabletType]string {
	switch kind {
	case bson.Object:
		//valid
	case bson.Null:
		return nil
	default:
		panic(bson.NewBsonError("Unexpected data type %v for ServedFrom map", kind))
	}

	servedFrom := make(map[TabletType]string)
	bson.Next(buf, 4)
	for kind = bson.NextByte(buf); kind != bson.EOO; kind = bson.NextByte(buf) {
		keyName := bson.ReadCString(buf)
		switch kind {
		case bson.String, bson.Binary:
			servedFrom[TabletType(keyName)] = bson.DecodeString(buf, kind)
		}
	}
	return servedFrom
}

func (sk *SrvKeyspace) MarshalBson(buf *bytes2.ChunkedWriter, key string) {
	bson.EncodeOptionalPrefix(buf, bson.Object, key)
	lenWriter := bson.NewLenWriter(buf)

	EncodeKeyspacePartitionMap(buf, "Partitions", sk.Partitions)

	EncodeSrvShardArray(buf, "Shards", sk.Shards)

	EncodeTabletTypeArray(buf, "TabletTypes", sk.TabletTypes)
	bson.EncodeString(buf, "ShardingColumnName", sk.ShardingColumnName)
	bson.EncodeString(buf, "ShardingColumnType", string(sk.ShardingColumnType))
	EncodeServedFrom(buf, "ServedFrom", sk.ServedFrom)

	buf.WriteByte(0)
	lenWriter.RecordLen()
}

func (sk *SrvKeyspace) UnmarshalBson(buf *bytes.Buffer, kind byte) {
	bson.VerifyObject(kind)
	bson.Next(buf, 4)

	kind = bson.NextByte(buf)
	for kind != bson.EOO {
		keyName := bson.ReadCString(buf)
		switch keyName {
		case "Partitions":
			sk.Partitions = DecodeKeyspacePartitionMap(buf, kind)
		case "Shards":
			sk.Shards = DecodeSrvShardArray(buf, kind)
		case "TabletTypes":
			sk.TabletTypes = DecodeTabletTypeArray(buf, kind)
		case "ShardingColumnName":
			sk.ShardingColumnName = bson.DecodeString(buf, kind)
		case "ShardingColumnType":
			sk.ShardingColumnType = key.KeyspaceIdType(bson.DecodeString(buf, kind))
		case "ServedFrom":
			sk.ServedFrom = DecodeServedFrom(buf, kind)
		default:
			bson.Skip(buf, kind)
		}
		kind = bson.NextByte(buf)
	}
}
