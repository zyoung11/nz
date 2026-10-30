package com.nz.mobile_nz

import android.content.Intent
import android.net.VpnService
import com.nz.mobile_nz.nz.NzCrypto
import com.nz.mobile_nz.nz.NzProtocol
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetSocketAddress

class MainActivity : FlutterActivity() {
    private val CHANNEL = "nz_vpn"

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        NzVpnService.saveRunningState(this, false)

        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL).setMethodCallHandler { call, result ->
            when (call.method) {
                "start" -> {
                    val config = call.argument<String>("config")
                    if (config != null) {
                        startVpn(config, result)
                    } else {
                        result.error("INVALID_CONFIG", "Config is required", null)
                    }
                }
                "stop" -> {
                    stopVpn()
                    result.success(true)
                }
                "isRunning" -> {
                    result.success(isVpnRunning())
                }
                "getStatus" -> {
                    result.success(NzVpnService.instance?.getStatus() ?: "{}")
                }
                "getNodeList" -> {
                    Thread {
                        val config = NzVpnService.getConfig(this@MainActivity)
                        if (config != null) {
                            val list = getNodeListDirect(config)
                            result.success(list)
                        } else {
                            result.success("[]")
                        }
                    }.start()
                }
                else -> result.notImplemented()
            }
        }
    }

    private var pendingConfig: String? = null
    private var pendingResult: MethodChannel.Result? = null

    private fun startVpn(config: String, result: MethodChannel.Result) {
        val intent = VpnService.prepare(this)
        if (intent != null) {
            pendingConfig = config
            pendingResult = result
            startActivityForResult(intent, 100)
        } else {
            launchVpnService(config)
            result.success(true)
        }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 100) {
            if (resultCode == RESULT_OK && pendingConfig != null) {
                launchVpnService(pendingConfig!!)
                pendingResult?.success(true)
            } else {
                pendingResult?.success(false)
            }
            pendingConfig = null
            pendingResult = null
        }
    }

    private fun launchVpnService(config: String) {
        val intent = Intent(this, NzVpnService::class.java).apply {
            action = "START"
            putExtra("config", config)
        }
        startForegroundService(intent)
    }

    private fun stopVpn() {
        val intent = Intent(this, NzVpnService::class.java).apply {
            action = "STOP"
        }
        startService(intent)
    }

    private fun isVpnRunning(): Boolean {
        return NzVpnService.isRunning(this)
    }

    private fun getNodeListDirect(configJson: String): String {
        try {
            val map = configJson.removeSurrounding("{", "}")
                .split(",")
                .map { it.trim() }
                .filter { it.isNotEmpty() }
                .associate {
                    val (key, value) = it.split(":", limit = 2)
                    key.trim().removeSurrounding("\"") to value.trim().removeSurrounding("\"")
                }

            val password = map["password"] ?: return "[]"
            val domain = map["domain"] ?: return "[]"
            val port = map["port"]?.toIntOrNull() ?: 4242

            val key = NzCrypto.sharedKey(password)

            val addr = java.net.InetAddress.getByName(domain)
            val server = InetSocketAddress(addr, port)

            val socket = DatagramSocket(0)
            socket.soTimeout = 3000

            val nonce = NzCrypto.genNonce()
            val encPayload = NzCrypto.encrypt(key, nonce)
            val listMsg = NzProtocol.marshalMessage(NzProtocol.MSG_PEER_LIST, encPayload)
            socket.send(DatagramPacket(listMsg, listMsg.size, server))

            val buf = ByteArray(65536)
            val packet = DatagramPacket(buf, buf.size)
            socket.receive(packet)
            socket.close()

            val msg = NzProtocol.unmarshalMessage(buf.copyOf(packet.length))
            if (msg == null || msg.type != NzProtocol.MSG_PEER_LIST_RPY) {
                return "[]"
            }

            val decData = NzCrypto.decrypt(key, msg.payload)
            return String(decData)
        } catch (e: Exception) {
            return "[]"
        }
    }
}
