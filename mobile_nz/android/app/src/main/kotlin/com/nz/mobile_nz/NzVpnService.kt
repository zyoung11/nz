package com.nz.mobile_nz

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.core.app.NotificationCompat
import com.nz.mobile_nz.nz.NzClient
import com.nz.mobile_nz.nz.NzProtocol
import java.io.FileInputStream
import java.io.FileOutputStream

class NzVpnService : VpnService() {
    private var vpnInterface: ParcelFileDescriptor? = null
    var nzClient: NzClient? = null
        private set
    private var tunReaderThread: Thread? = null
    private var running = false
    private val TAG = "NzVpnService"

    companion object {
        private const val CHANNEL_ID = "nz_vpn_channel"
        private const val NOTIFICATION_ID = 1
        var instance: NzVpnService? = null
            private set

        private val ALWAYS_EXCLUDED_APPS = listOf(
            "com.google.android.projection.gearhead",
            "com.google.android.apps.chromecast.app",
            "com.google.android.apps.messaging"
        )

        fun getConfig(context: android.content.Context): String? {
            val vpnConfig = context.getSharedPreferences("nz_vpn_config", android.content.Context.MODE_PRIVATE)
                .getString("config_json", null)
            if (vpnConfig != null) return vpnConfig

            val prefs = context.getSharedPreferences("FlutterSharedPreferences", android.content.Context.MODE_PRIVATE)
            val flutterConfig = prefs.getString("flutter.nz_config", null)
            return flutterConfig
        }

        fun saveConfig(context: android.content.Context, configJson: String) {
            context.getSharedPreferences("nz_vpn_config", android.content.Context.MODE_PRIVATE)
                .edit().putString("config_json", configJson).apply()
        }

        fun saveRunningState(context: android.content.Context, running: Boolean) {
            context.getSharedPreferences("nz_vpn_state", android.content.Context.MODE_MULTI_PROCESS)
                .edit().putBoolean("is_running", running).commit()
        }

        fun isRunning(context: android.content.Context): Boolean {
            return context.getSharedPreferences("nz_vpn_state", android.content.Context.MODE_MULTI_PROCESS)
                .getBoolean("is_running", false)
        }
    }

    private var networkCallback: ConnectivityManager.NetworkCallback? = null

    override fun onCreate() {
        super.onCreate()
        instance = this
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForeground(NOTIFICATION_ID, createNotification("Connecting..."), ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)

        when (intent?.action) {
            "START" -> {
                val config = intent.getStringExtra("config")
                if (config != null) {
                    startVpn(config)
                }
            }
            "STOP" -> stopVpn()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        stopVpn()
        instance = null
        super.onDestroy()
    }

    private fun startVpn(configJson: String) {
        try {
            val config = parseConfig(configJson)
            if (config == null) {
                Log.e(TAG, "Invalid config")
                stopSelf()
                return
            }

            saveConfig(this, configJson)

            val builder = Builder()
                .setMtu(1500)
                .addAddress("192.168.100.${config.ip}", 24)
                .addRoute("192.168.100.0", 24)
                .setSession("nz")

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                builder.setMetered(false)
            }

            disallowApp(builder, "com.nz.mobile_nz")
            disallowApp(builder, "com.nz.mobile_nz.debug")

            ALWAYS_EXCLUDED_APPS.forEach { disallowApp(builder, it) }

            vpnInterface = builder.establish()

            if (vpnInterface == null) {
                Log.e(TAG, "Failed to establish VPN interface")
                stopSelf()
                return
            }

            Log.i(TAG, "VPN interface established")
            updateNotification("Connecting...")

            running = true
            saveRunningState(this, true)

            val clientConfig = NzClient.NzConfig(
                name = config.name,
                password = config.password,
                domain = config.domain,
                ip = config.ip,
                port = config.port
            )

            nzClient = NzClient(clientConfig, object : NzClient.StatusCallback {
                override fun onStatusChanged(status: NzClient.NzStatus) {
                    Log.d(TAG, "Status: ${status.state} ${status.vpnIP}")
                    when (status.state) {
                        "connected" -> updateNotification("Connected: ${status.vpnIP}")
                        "error" -> updateNotification("Error: ${status.error}")
                        "disconnected" -> updateNotification("Disconnected")
                    }
                }
            })

            nzClient?.setTunWriter { data ->
                writePacketToTun(data)
            }

            Thread({
                if (!nzClient!!.start()) {
                    Log.e(TAG, "Failed to start NZ client")
                    stopVpn()
                } else {
                    startTunReader()
                }
            }, "nz-start").start()

            startTunReader()

            Thread {
                Thread.sleep(5000)
                registerNetworkCallback()
                Log.i(TAG, "Network callback registered")
            }.start()

            Log.i(TAG, "NZ VPN started")

        } catch (e: Exception) {
            Log.e(TAG, "Failed to start VPN", e)
            stopVpn()
        }
    }

    private fun disallowApp(builder: Builder, name: String) {
        try {
            builder.addDisallowedApplication(name)
        } catch (e: PackageManager.NameNotFoundException) {
            return
        }
    }

    private fun registerNetworkCallback() {
        val connectivityManager = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()

        networkCallback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                super.onAvailable(network)
                Log.i(TAG, "Network changed, reconnecting...")
                nzClient?.reconnect()
            }

            override fun onLost(network: Network) {
                super.onLost(network)
                Log.i(TAG, "Network lost, reconnecting...")
                nzClient?.reconnect()
            }
        }

        connectivityManager.registerNetworkCallback(request, networkCallback!!)
    }

    private fun unregisterNetworkCallback() {
        if (networkCallback != null) {
            try {
                val connectivityManager = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
                connectivityManager.unregisterNetworkCallback(networkCallback!!)
            } catch (e: Exception) {
                Log.w(TAG, "Failed to unregister network callback: ${e.message}")
            }
            networkCallback = null
        }
    }

    private fun stopVpn() {
        running = false

        unregisterNetworkCallback()

        tunReaderThread?.interrupt()
        try {
            tunReaderThread?.join(1000)
        } catch (_: InterruptedException) {}
        tunReaderThread = null

        nzClient?.stop()
        nzClient = null

        try {
            vpnInterface?.close()
        } catch (e: Exception) {
            Log.e(TAG, "Error closing VPN interface", e)
        }
        vpnInterface = null

        saveRunningState(this, false)

        stopForeground(true)
        stopSelf()
    }

    private fun startTunReader() {
        val tunFd = vpnInterface?.fileDescriptor ?: return

        tunReaderThread = Thread({
            val inputStream = FileInputStream(tunFd)
            val buffer = ByteArray(65536)

            Log.i(TAG, "TUN reader started")

            while (running) {
                try {
                    val nread = inputStream.read(buffer)
                    if (nread <= 0) continue

                    val packet = buffer.copyOf(nread)

                    if (packet.size < 20 || (packet[0].toInt() and 0xF0) != 0x40) {
                        continue
                    }

                    val dstIP = byteArrayOf(packet[16], packet[17], packet[18], packet[19])
                    val dstIPStr = NzProtocol.ipToString(dstIP)

                    Log.d(TAG, "TUN packet: $nread bytes, dst=$dstIPStr")

                    if (!dstIPStr.startsWith("192.168.100.")) {
                        continue
                    }

                    nzClient?.sendPacket(dstIP, packet)

                } catch (e: Exception) {
                    if (running) {
                        Log.e(TAG, "TUN read error", e)
                    }
                }
            }

            try {
                inputStream.close()
            } catch (_: Exception) {
            }

        }, "nz-tun-reader")

        tunReaderThread?.start()
    }

    private fun writePacketToTun(data: ByteArray) {
        try {
            val tunFd = vpnInterface?.fileDescriptor ?: return
            val outputStream = FileOutputStream(tunFd)
            outputStream.write(data)
            outputStream.flush()
            Log.d(TAG, "TUN write: ${data.size} bytes")
        } catch (e: Exception) {
            Log.e(TAG, "TUN write error", e)
        }
    }

    private fun parseConfig(json: String): Config? {
        return try {
            val map = json.removeSurrounding("{", "}")
                .split(",")
                .map { it.trim() }
                .filter { it.isNotEmpty() }
                .associate {
                    val (key, value) = it.split(":", limit = 2)
                    key.trim().removeSurrounding("\"") to value.trim().removeSurrounding("\"")
                }

            Config(
                name = map["name"] ?: return null,
                password = map["password"] ?: return null,
                domain = map["domain"] ?: return null,
                ip = map["ip"]?.toIntOrNull() ?: 2,
                port = map["port"]?.toIntOrNull() ?: 4242
            )
        } catch (e: Exception) {
            Log.e(TAG, "Failed to parse config", e)
            null
        }
    }

    data class Config(
        val name: String,
        val password: String,
        val domain: String,
        val ip: Int,
        val port: Int
    )

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "NZ VPN",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "NZ VPN Service"
            }
            val notificationManager = getSystemService(NotificationManager::class.java)
            notificationManager.createNotificationChannel(channel)
        }
    }

    private fun createNotification(status: String): Notification {
        val pendingIntent = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("NZ VPN")
            .setContentText(status)
            .setSmallIcon(R.mipmap.ic_launcher)
            .setContentIntent(pendingIntent)
            .setOngoing(true)
            .build()
    }

    private fun updateNotification(status: String) {
        val notificationManager = getSystemService(NotificationManager::class.java)
        notificationManager.notify(NOTIFICATION_ID, createNotification(status))
    }

    fun isRunning(): Boolean = running

    fun getStatus(): String {
        return if (running) """{"state":"connected"}""" else """{"state":"disconnected"}"""
    }
}
