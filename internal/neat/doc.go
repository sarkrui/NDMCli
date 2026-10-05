// Package neat reads a local Neat Download Manager database and turns a
// saved job into a request that curl or this process can run.
//
// macOS builds of Neat Download Manager 1.3 keep that database at
// ~/Library/Application Support/com.NeatDownloadManager/NeatDB.db.
//
//	downloads  url, method, file name, user agent, POST body, size, status
//	headers    one request header per row ("Cookie: ...", "Referer: ...")
//
// The cookie is the Cookie header text captured with the download. There
// is no separate cookie jar. Partial multi-connection jobs also leave
// <id>/segments.bin and <id>/seg.xN next to the database. segments.bin is
// a sequence of 24-byte little-endian records:
//
//	u16 index | u16 index | u32 state | u64 start | u64 end
//
// end is inclusive. seg.xN holds the bytes already stored for that range,
// beginning at start. A state of 0xffffffff means that segment had no
// live connection; it can still contain bytes.
package neat
