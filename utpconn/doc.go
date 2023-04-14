// Package utp implements uTP, the micro transport protocol as used with
// Bittorrent. It opts for simplicity and reliability over strict adherence to
// the (poor) spec. It allows using the underlying OS-level transport despite
// dispatching uTP on top to allow for example, shared socket use with DHT.
// Additionally, multiple uTP connections can share the same OS socket, to
// truly realize uTP's claim to be light on system and network switching
// resources.
//
// Socket is a wrapper of net.PacketConn, and performs dispatching of uTP packets
// to attached uTP Conns. Dial and Accept is done via Socket. Conn implements
// net.Conn over uTP, via aforementioned Socket.
package utpconn
