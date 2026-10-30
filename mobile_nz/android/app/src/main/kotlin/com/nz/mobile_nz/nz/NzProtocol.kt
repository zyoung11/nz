package com.nz.mobile_nz.nz

import java.nio.ByteBuffer
import java.nio.ByteOrder

object NzProtocol {
    const val PROTOCOL_VERSION: Byte = 0x02

    // Message types
    const val MSG_HELLO: Byte = 0x01
    const val MSG_HELLO_REPLY: Byte = 0x02
    const val MSG_CONFIRM: Byte = 0x03
    const val MSG_CONFIRM_REPLY: Byte = 0x04
    const val MSG_PEER_LIST: Byte = 0x12
    const val MSG_PEER_LIST_RPY: Byte = 0x13
    const val MSG_KEEPALIVE: Byte = 0x0E
    const val MSG_DISCONNECT: Byte = 0x14

    const val MSG_DATA: Byte = 0x20
    const val MSG_RELAY_DATA: Byte = 0x30
    const val MSG_ERROR: Byte = 0xFF.toByte()

    data class Message(val type: Byte, val payload: ByteArray)

    fun marshalMessage(type: Byte, payload: ByteArray): ByteArray {
        val buf = ByteArray(4 + payload.size)
        buf[0] = PROTOCOL_VERSION
        buf[1] = type
        ByteBuffer.wrap(buf, 2, 2).order(ByteOrder.BIG_ENDIAN).putShort(payload.size.toShort())
        System.arraycopy(payload, 0, buf, 4, payload.size)
        return buf
    }

    fun unmarshalMessage(raw: ByteArray): Message? {
        if (raw.size < 4) return null
        if (raw[0] != PROTOCOL_VERSION) return null

        val payloadLen = ByteBuffer.wrap(raw, 2, 2).order(ByteOrder.BIG_ENDIAN).short.toInt() and 0xFFFF
        if (raw.size < 4 + payloadLen) return null

        val payload = raw.copyOfRange(4, 4 + payloadLen)
        return Message(raw[1], payload)
    }

    fun marshalHello(name: String, desiredIP: Int, clientNonce: ByteArray): ByteArray {
        val nameBytes = name.toByteArray()
        val buf = ByteArray(2 + nameBytes.size + clientNonce.size)
        buf[0] = nameBytes.size.toByte()
        buf[1] = desiredIP.toByte()
        System.arraycopy(nameBytes, 0, buf, 2, nameBytes.size)
        System.arraycopy(clientNonce, 0, buf, 2 + nameBytes.size, clientNonce.size)
        return buf
    }

    data class HelloReply(val encClientNonce: ByteArray, val serverNonce: ByteArray)

    fun unmarshalHelloReply(payload: ByteArray): HelloReply? {
        if (payload.size < 2) return null
        val encLen = payload[0].toInt() and 0xFF
        val srvLen = payload[1].toInt() and 0xFF
        if (payload.size < 2 + encLen + srvLen) return null

        val encClientNonce = payload.copyOfRange(2, 2 + encLen)
        val serverNonce = payload.copyOfRange(2 + encLen, 2 + encLen + srvLen)
        return HelloReply(encClientNonce, serverNonce)
    }

    data class ConfirmReply(val vpnIP: ByteArray, val netmask: Int)

    fun unmarshalConfirmReply(payload: ByteArray): ConfirmReply? {
        if (payload.size < 5) return null
        val vpnIP = payload.copyOfRange(0, 4)
        val netmask = payload[4].toInt() and 0xFF
        return ConfirmReply(vpnIP, netmask)
    }

    fun marshalRelayData(srcIP: ByteArray, targetIP: ByteArray, data: ByteArray): ByteArray {
        val buf = ByteArray(8 + data.size)
        System.arraycopy(srcIP, 0, buf, 0, 4)
        System.arraycopy(targetIP, 0, buf, 4, 4)
        System.arraycopy(data, 0, buf, 8, data.size)
        return buf
    }

    data class RelayData(val srcIP: ByteArray, val targetIP: ByteArray, val data: ByteArray)

    fun unmarshalRelayData(payload: ByteArray): RelayData? {
        if (payload.size < 8) return null
        val srcIP = payload.copyOfRange(0, 4)
        val targetIP = payload.copyOfRange(4, 8)
        val data = payload.copyOfRange(8, payload.size)
        return RelayData(srcIP, targetIP, data)
    }

    fun ipToString(ip: ByteArray): String {
        return "${ip[0].toInt() and 0xFF}.${ip[1].toInt() and 0xFF}.${ip[2].toInt() and 0xFF}.${ip[3].toInt() and 0xFF}"
    }

    fun stringToIP(ip: String): ByteArray {
        val parts = ip.split(".")
        return byteArrayOf(
            parts[0].toInt().toByte(),
            parts[1].toInt().toByte(),
            parts[2].toInt().toByte(),
            parts[3].toInt().toByte()
        )
    }
}
