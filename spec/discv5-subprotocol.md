# Discv5 Sub-Protocol Connections

This is a draft spec for a discv5 protocol extension that supports transferring arbitrary
sub-protocol data over an encrypted connection.

## Session Table

Implementations should keep a *sub-protocol session table*, containing session records.
Sessions are identified by the IP address of the remote node and the `ingress-id` value.
Inactive sessions are removed from the table as they time out. Suitable session timeouts
depend on the sub-protocol.

A session record contains:

- `ip`, the IP address of the remote node
- `ingress-id`, used to locate the session
- `ingress-key`, used for decrypting received packets
- `egress-id`, used as the session ID value in sent packets
- `egress-key` and `nonce-counter`, used for encrypting sent packets
- the sub-protocol that initiated the session

## Establishing a Session

It is expected that sessions will be established through an existing encrypted and
authenticated channel, such as discv5 TALKREQ/TALKRESP. **There is no in-band way to
create a session.**

We assume that the implementation provides a procedure `newsession` which derives keys and
creates a new entry in the table. Keys and ID values are created as follows.

    newsession(initiator-secret, recipient-secret, protocol-name)
        initiator-secret :: bytes16
        recipient-secret :: bytes16
        protocol-name :: bytes

        ikm    = initiator-secret || recipient-secret
        salt   = ""
        info   = "discv5 sub-protocol session" || protocol-name
        length = 48
        kdata  = HKDF(salt, ikm, info, length)

        initiator-key = kdata[0:16]
        recipient-key = kdata[16:32]
        initiator-id = kdata[32:40]
        recipient-id = kdata[40:48]

        When called on the initiator side:
          egress-id, egress-key = recipient-id, recipient-key
          ingress-id, ingress-key = initiator-id, initiator-key

        When called on the recipient side:
          egress-id, egress-key = initiator-id, initiator-key
          ingress-id, ingress-key = recipient-id, recipient-key

In order to establish a sub-protocol session, the initiator creates its
`initiator-secret` using a secure random number generator. It sends an appropriate
TALKREQ message containing `initiator-secret` and any other information necessary for
requesting a sub-protocol connection.

If the recipient agrees with the creation of the connection, it generates the
`recipient-secret` and calls `newsession()` to create a session. It then sends an
affirmative TALKRESP message containing the `recipient-key`, and possibly other
sub-protocol specific data.

When the initiator receives TALKRESP containing the `recipient-secret`, it also calls
`newsession()` to create and store the session. At this point the session is established
packets can be sent in both directions.

Note that the first sub-protocol packet must be sent by the session initiator, since
the session recipient doesn't know if and when the TALKRESP message will arrive. This
limitation can be inconvenient for sub-protocols using a request/response scheme
where data is to be served by the session recipient immediately after establishment.
The first sub-protocol packet can have an empty payload in this case, but it really
must be sent to confirm validity of the session.

The listing below shows an example packet exchange where node `A` is the initiator
and node `B` is the recipient.

    A -> B  TALKREQ (... initiator-secret ...)
    A <- B  TALKRESP (... recipient-secret ...)
    A -> B  sub-protocol packet
    A <- B  sub-protocol packet
    A <- B  sub-protocol packet
    ...

## Packets

Sub-protocol packets have a simple structure with total overhead of 36 bytes,
including the GCM tag (which is a part of `ciphertext`).

    packet = session-id || nonce || ciphertext
      session-id :: uint64
      nonce      :: uint96
      ciphertext :: bytes

To send a sub-protocol packet for an existing session, the `session-id` of the packet
is assigned from the `egress-id` of the session. `nonce` is selected by incrementing
the session's `nonce-counter` value. It is recommended to also fill a part of the
`nonce` using a secure random number generator. Now the ciphertext is created:

    ciphertext = aesgcm_encrypt(egress-key, nonce, payload, egress-id)

When the node receives a UDP packet, it first checks that the packet data has a
length of at least 20 bytes. It then performs a lookup into the *sub-protocol session
table* by interpreting the first 8 bytes of the packets as a `session-id`. This value
is used to look for an active session with a matching `ingress-id` and IP address
value.

If there is no matching session, the packet is considered off-protocol and is
submitted for processing as a regular discv5 packet.

If a session exists, the node performs AES/GCM decryption/authentication. Packets
failing this step are discarded. If authentication succeeds, the session's idle timer
is extended and the decrypted plaintext is dispatched to the sub-protocol
implementation.

#### Security Considerations

This section explores some of the design choices from a security point-of-view.

- The key agreement scheme assumes an existing encrypted and authenticated
  communication channel. As such, key material is passed directly between
  participants. Any breach of session keys for this channel is also a breach of
  sub-protocol session keys.

- Both parties contribute key material used for session identifiers and keys. This is
  done to ensure that plain-text packet data cannot be predetermined or assigned with
  malicious intent by the initiator or recipient. It's also convenient because only a
  single value needs to be communicated across during session establishment.

- Sessions can be created with little overhead. Implementations should place limits
  on the number of concurrent sessions that can be created. It is good practice to
  have a limit on the total number of active sessions, because an attacker could use
  a large number of nodes to work around address-based limits.

- Since `session-id` values are transmitted plain-text, an observer in a privileged
  network position will be able to determine which packets belong to a single session.

- The protocol does not provide any ordering or transfer reliability guarantees.
  Sub-protocols are expected to provide such guarantees if needed.
