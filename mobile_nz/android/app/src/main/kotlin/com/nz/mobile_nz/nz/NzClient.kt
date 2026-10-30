package com.nz.mobile_nz.nz

import android.util.Log
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.nio.channels.DatagramChannel
import java.util.concurrent.atomic.AtomicBoolean

class NzClient(
    private val config: NzConfig,
    private val callback: StatusCallback
) {
    companion object {
        private const val TAG = "NzClient"
        private const val KEEPALIVE_INTERVAL = 5_000L
        private const val HANDSHAKE_TIMEOUT = 5_000L
        private const val MAX_RETRIES = 3
    }

    interface StatusCallback {
        fun onStatusChanged(status: NzStatus)
    }

    data class NzConfig(
        val name: String,
        val password: String,
        val domain: String,
        val ip: Int,
        val port: Int = 4242
    )

    data class NzStatus(
        val state: String,
        val vpnIP: String = "",
        val error: String = ""
    )

    private val key = NzCrypto.sharedKey(config.password)
    private var socket: DatagramSocket? = null
    private var vpnIP: ByteArray? = null
    private var serverAddr: InetSocketAddress? = null
    private val running = AtomicBoolean(false)
    private var keepaliveThread: Thread? = null
    private var readerThread: Thread? = null
    private var tunWriter: ((ByteArray) -> Unit)? = null

    fun setTunWriter(writer: (ByteArray) -> Unit) {
        tunWriter = writer
    }

    fun start(): Boolean {
        if (running.getAndSet(true)) return false

        try {
            val channel = DatagramChannel.open()
            channel.configureBlocking(true)
            socket = channel.socket()
            socket!!.reuseAddress = true
            socket!!.bind(InetSocketAddress(0))
            socket!!.soTimeout = HANDSHAKE_TIMEOUT.toInt()

            protectSocket(channel)

            Log.i(TAG, "Socket created, local port: ${socket!!.localPort}")

            updateStatus("connecting")

            if (!handshake()) {
                updateStatus("error", error = "Handshake failed")
                stop()
                return false
            }

            updateStatus("connected", vpnIP = NzProtocol.ipToString(vpnIP!!))

            // Start keepalive thread
            keepaliveThread = Thread(::keepaliveLoop, "nz-keepalive").apply { start() }

            // Start reader thread
            readerThread = Thread(::readerLoop, "nz-reader").apply { start() }

            return true
        } catch (e: Exception) {
            Log.e(TAG, "Failed to start", e)
            updateStatus("error", error = e.message ?: "Unknown error")
            stop()
            return false
        }
    }

    fun reconnect() {
        if (!running.get()) return
        if (vpnIP != null) {
            Log.d(TAG, "Already connected, skipping reconnect")
            return
        }

        Log.i(TAG, "Reconnecting...")

        try {
            socket?.close()
        } catch (e: Exception) {
            Log.w(TAG, "Error closing socket: ${e.message}")
        }

        socket = null
        vpnIP = null
        serverAddr = null

        keepaliveThread?.interrupt()
        keepaliveThread = null

        readerThread?.interrupt()
        readerThread = null

        updateStatus("connecting")

        Thread {
            if (handshake()) {
                updateStatus("connected", vpnIP = NzProtocol.ipToString(vpnIP!!))
                keepaliveThread = Thread(::keepaliveLoop, "nz-keepalive").apply { start() }
                readerThread = Thread(::readerLoop, "nz-reader").apply { start() }
            } else {
                updateStatus("error", error = "Reconnect failed")
            }
        }.start()
    }

    fun stop() {
        if (!running.getAndSet(false)) return

        try {
            if (serverAddr != null && vpnIP != null) {
                val msg = NzProtocol.marshalMessage(NzProtocol.MSG_DISCONNECT, ByteArray(0))
                socket?.send(DatagramPacket(msg, msg.size, serverAddr))
                Log.i(TAG, "Sent disconnect")
                Thread.sleep(100)
            }
        } catch (e: Exception) {
            Log.e(TAG, "Error sending disconnect", e)
        }

        socket?.close()
        socket = null

        keepaliveThread?.interrupt()
        try {
            keepaliveThread?.join(1000)
        } catch (_: InterruptedException) {}
        keepaliveThread = null

        readerThread?.interrupt()
        try {
            readerThread?.join(1000)
        } catch (_: InterruptedException) {}
        readerThread = null

        updateStatus("disconnected")
    }

    fun sendPacket(targetIP: ByteArray, data: ByteArray) {
        if (!running.get()) {
            Log.w(TAG, "sendPacket: not running")
            return
        }

        val srcIP = vpnIP ?: run {
            Log.w(TAG, "sendPacket: no vpnIP")
            return
        }
        val server = serverAddr ?: run {
            Log.w(TAG, "sendPacket: no serverAddr")
            return
        }

        try {
            val relayPayload = NzProtocol.marshalRelayData(srcIP, targetIP, data)
            val encPayload = NzCrypto.encrypt(key, relayPayload)
            val msg = NzProtocol.marshalMessage(NzProtocol.MSG_RELAY_DATA, encPayload)
            socket?.send(DatagramPacket(msg, msg.size, server))
            Log.d(TAG, "Sent relay packet: ${data.size} bytes to ${NzProtocol.ipToString(targetIP)}")
        } catch (e: Exception) {
            Log.e(TAG, "Error sending packet", e)
        }
    }

    private fun handshake(): Boolean {
        val server = resolveServer() ?: run {
            Log.e(TAG, "Failed to resolve server")
            return false
        }
        serverAddr = server
        Log.i(TAG, "Server resolved: $server")

        val clientNonce = NzCrypto.genNonce()
        Log.d(TAG, "Client nonce: ${clientNonce.size} bytes")

        // Send Hello
        val helloPayload = NzProtocol.marshalHello(config.name, config.ip, clientNonce)
        val helloMsg = NzProtocol.marshalMessage(NzProtocol.MSG_HELLO, helloPayload)
        Log.i(TAG, "Sending Hello: name=${config.name}, ip=${config.ip}")

        for (retry in 0 until MAX_RETRIES) {
            try {
                socket?.send(DatagramPacket(helloMsg, helloMsg.size, server))
                Log.d(TAG, "Hello sent, waiting for reply...")

                // Wait for HelloReply
                val buf = ByteArray(65536)
                val packet = DatagramPacket(buf, buf.size)
                socket?.receive(packet)
                Log.d(TAG, "Received reply: ${packet.length} bytes")

                Log.d(TAG, "Raw data first 20 bytes: ${buf.take(20).joinToString(" ") { "%02x".format(it) }}")
                val msg = NzProtocol.unmarshalMessage(buf.copyOf(packet.length)) ?: run {
                    Log.w(TAG, "Failed to unmarshal message")
                    continue
                }
                Log.d(TAG, "Message type: 0x${msg.type.toString(16)}")

                if (msg.type != NzProtocol.MSG_HELLO_REPLY) {
                    Log.w(TAG, "Expected HelloReply, got 0x${msg.type.toString(16)}")
                    continue
                }

                val reply = NzProtocol.unmarshalHelloReply(msg.payload) ?: run {
                    Log.w(TAG, "Failed to unmarshal HelloReply")
                    continue
                }
                Log.d(TAG, "HelloReply: encClientNonce=${reply.encClientNonce.size}, serverNonce=${reply.serverNonce.size}")

                // Verify client nonce
                val decClientNonce = NzCrypto.decrypt(key, reply.encClientNonce)
                if (!decClientNonce.contentEquals(clientNonce)) {
                    Log.e(TAG, "Client nonce mismatch")
                    continue
                }
                Log.d(TAG, "Client nonce verified")

                // Send Confirm
                val encServerNonce = NzCrypto.encrypt(key, reply.serverNonce)
                val confirmMsg = NzProtocol.marshalMessage(NzProtocol.MSG_CONFIRM, encServerNonce)
                socket?.send(DatagramPacket(confirmMsg, confirmMsg.size, server))
                Log.i(TAG, "Sent Confirm")

                // Wait for ConfirmReply
                socket?.receive(packet)
                Log.d(TAG, "Received ConfirmReply: ${packet.length} bytes")

                val confirmReplyMsg = NzProtocol.unmarshalMessage(buf.copyOf(packet.length)) ?: run {
                    Log.w(TAG, "Failed to unmarshal ConfirmReply")
                    continue
                }

                if (confirmReplyMsg.type != NzProtocol.MSG_CONFIRM_REPLY) {
                    Log.w(TAG, "Expected ConfirmReply, got 0x${confirmReplyMsg.type.toString(16)}")
                    continue
                }

                val decPayload = NzCrypto.decrypt(key, confirmReplyMsg.payload)
                val confirmReply = NzProtocol.unmarshalConfirmReply(decPayload) ?: run {
                    Log.w(TAG, "Failed to unmarshal ConfirmReply payload")
                    continue
                }

                vpnIP = confirmReply.vpnIP
                Log.i(TAG, "Handshake successful, VPN IP: ${NzProtocol.ipToString(vpnIP!!)}")

                // Set socket timeout for normal operation
                socket?.soTimeout = 0
                return true

            } catch (e: Exception) {
                Log.w(TAG, "Handshake retry $retry failed", e)
            }
        }

        Log.e(TAG, "Handshake failed after $MAX_RETRIES retries")
        return false
    }

    private fun resolveServer(): InetSocketAddress? {
        return try {
            Log.d(TAG, "Resolving server: ${config.domain}")
            val addr = InetAddress.getByName(config.domain)
            Log.d(TAG, "Server resolved to: ${addr.hostAddress}")
            InetSocketAddress(addr, config.port)
        } catch (e: Exception) {
            Log.e(TAG, "Failed to resolve server", e)
            null
        }
    }

    private fun keepaliveLoop() {
        Log.i(TAG, "Keepalive thread started")

        while (running.get()) {
            try {
                Thread.sleep(KEEPALIVE_INTERVAL)

                if (!running.get()) break

                val ip = vpnIP ?: continue
                val server = serverAddr ?: continue

                val kaMsg = NzProtocol.marshalMessage(NzProtocol.MSG_KEEPALIVE, ip)
                socket?.send(DatagramPacket(kaMsg, kaMsg.size, server))
                Log.d(TAG, "Sent keepalive")

            } catch (e: InterruptedException) {
                Log.d(TAG, "Keepalive thread interrupted")
                break
            } catch (e: Exception) {
                Log.e(TAG, "Keepalive error", e)
            }
        }

        Log.i(TAG, "Keepalive thread stopped")
    }

    private fun readerLoop() {
        val buf = ByteArray(65536)

        Log.i(TAG, "Reader thread started")

        while (running.get()) {
            try {
                val packet = DatagramPacket(buf, buf.size)
                socket?.receive(packet)

                if (!running.get()) break

                Log.d(TAG, "Raw data first 20 bytes: ${buf.take(20).joinToString(" ") { "%02x".format(it) }}")
                val msg = NzProtocol.unmarshalMessage(buf.copyOf(packet.length)) ?: run {
                    Log.w(TAG, "Failed to unmarshal received message")
                    continue
                }

                Log.d(TAG, "Received message type: 0x${msg.type.toString(16)}, size: ${msg.payload.size}")

                when (msg.type) {
                    NzProtocol.MSG_RELAY_DATA -> {
                        Log.i(TAG, "Received relay data: ${msg.payload.size} bytes")
                        handleRelayData(msg.payload)
                    }
                    NzProtocol.MSG_DATA -> {
                        Log.i(TAG, "Received direct data: ${msg.payload.size} bytes")
                        handleDirectData(msg.payload)
                    }
                    NzProtocol.MSG_KEEPALIVE -> {
                        Log.d(TAG, "Received keepalive")
                    }
                    else -> {
                        Log.w(TAG, "Unknown message type: 0x${msg.type.toString(16)}")
                    }
                }

            } catch (e: Exception) {
                if (running.get()) {
                    Log.e(TAG, "Reader error", e)
                }
            }
        }

        Log.i(TAG, "Reader thread stopped")
    }

    private fun handleRelayData(payload: ByteArray) {
        try {
            val decPayload = NzCrypto.decrypt(key, payload)
            Log.d(TAG, "Decrypted relay data: ${decPayload.size} bytes")

            val relayData = NzProtocol.unmarshalRelayData(decPayload) ?: run {
                Log.w(TAG, "Failed to unmarshal relay data")
                return
            }

            Log.d(TAG, "Relay data: src=${NzProtocol.ipToString(relayData.srcIP)}, dst=${NzProtocol.ipToString(relayData.targetIP)}, data=${relayData.data.size} bytes")

            // Write to TUN
            tunWriter?.invoke(relayData.data)
            Log.d(TAG, "Wrote relay data to TUN")

        } catch (e: Exception) {
            Log.e(TAG, "Error handling relay data", e)
        }
    }

    private fun handleDirectData(payload: ByteArray) {
        try {
            val decPayload = NzCrypto.decrypt(key, payload)
            Log.d(TAG, "Decrypted direct data: ${decPayload.size} bytes")

            // Direct data format: srcIP (4 bytes) + raw packet
            if (decPayload.size < 4) {
                Log.w(TAG, "Direct data too short")
                return
            }

            val srcIP = decPayload.copyOfRange(0, 4)
            val data = decPayload.copyOfRange(4, decPayload.size)

            Log.d(TAG, "Direct data: src=${NzProtocol.ipToString(srcIP)}, data=${data.size} bytes")

            // Write to TUN
            tunWriter?.invoke(data)
            Log.d(TAG, "Wrote direct data to TUN")

        } catch (e: Exception) {
            Log.e(TAG, "Error handling direct data", e)
        }
    }

    private fun updateStatus(state: String, vpnIP: String = "", error: String = "") {
        Log.d(TAG, "Status: state=$state, vpnIP=$vpnIP, error=$error")
        callback.onStatusChanged(NzStatus(state, vpnIP, error))
    }

    fun getNodeList(): String {
        val server = serverAddr ?: run {
            Log.e(TAG, "getNodeList: no serverAddr")
            return "[]"
        }

        Log.i(TAG, "getNodeList: sending request to $server")

        var listChannel: DatagramChannel? = null
        try {
            listChannel = DatagramChannel.open()
            listChannel.configureBlocking(true)
            val listSocket = listChannel.socket()
            listSocket.reuseAddress = true
            listSocket.bind(InetSocketAddress(0))
            listSocket.soTimeout = 3000

            protectSocket(listChannel)

            val nonce = NzCrypto.genNonce()
            val encPayload = NzCrypto.encrypt(key, nonce)
            val listMsg = NzProtocol.marshalMessage(NzProtocol.MSG_PEER_LIST, encPayload)
            Log.d(TAG, "getNodeList: msg size=${listMsg.size}")
            listSocket.send(DatagramPacket(listMsg, listMsg.size, server))
            Log.i(TAG, "getNodeList: request sent")

            val buf = ByteArray(65536)
            val packet = DatagramPacket(buf, buf.size)
            listSocket.receive(packet)
            Log.i(TAG, "getNodeList: received ${packet.length} bytes")

            val msg = NzProtocol.unmarshalMessage(buf.copyOf(packet.length))
            Log.d(TAG, "getNodeList: msg type=${msg?.type}")

            if (msg == null || msg.type != NzProtocol.MSG_PEER_LIST_RPY) {
                Log.w(TAG, "getNodeList: unexpected response type")
                return "[]"
            }

            val decData = NzCrypto.decrypt(key, msg.payload)
            val result = String(decData)
            Log.i(TAG, "getNodeList: result=$result")
            return result
        } catch (e: Exception) {
            Log.e(TAG, "getNodeList failed: ${e.message}", e)
            return "[]"
        } finally {
            listChannel?.close()
        }
    }

    private fun protectSocket(channel: DatagramChannel) {
        try {
            Log.d(TAG, "protectSocket called")
            // Use reflection to get fd from AbstractSelectableChannel
            val clazz: Class<*>? = channel.javaClass.superclass
            val fdField = clazz?.getDeclaredField("fdVal")
            fdField?.isAccessible = true
            val fdInt = fdField?.getInt(channel) ?: -1
            if (fdInt >= 0) {
                com.nz.mobile_nz.NzVpnService.instance?.protect(fdInt)
                Log.d(TAG, "Socket protected successfully, fd=$fdInt")
            } else {
                Log.w(TAG, "Could not get fd from channel")
            }
        } catch (e: Exception) {
            Log.e(TAG, "protectSocket failed: ${e.message}", e)
        }
    }
}
