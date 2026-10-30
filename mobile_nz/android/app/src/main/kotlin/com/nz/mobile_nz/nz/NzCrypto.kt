package com.nz.mobile_nz.nz

import org.bouncycastle.crypto.generators.Argon2BytesGenerator
import org.bouncycastle.crypto.params.Argon2Parameters
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

object NzCrypto {
    private const val KEY_SIZE = 32
    private const val NONCE_SIZE = 24
    private const val GCM_NONCE_SIZE = 12
    private const val GCM_TAG_SIZE = 128

    private val GLOBAL_SALT = "nz-v1-global-salt-16".toByteArray()

    fun sharedKey(password: String): ByteArray {
        val params = Argon2Parameters.Builder(Argon2Parameters.ARGON2_id)
            .withSalt(GLOBAL_SALT)
            .withParallelism(4)
            .withMemoryAsKB(64 * 1024)
            .withIterations(3)
            .build()

        val generator = Argon2BytesGenerator()
        generator.init(params)
        val key = ByteArray(KEY_SIZE)
        generator.generateBytes(password.toByteArray(), key)
        return key
    }

    fun genNonce(): ByteArray {
        val nonce = ByteArray(NONCE_SIZE)
        SecureRandom().nextBytes(nonce)
        return nonce
    }

    fun encrypt(key: ByteArray, plaintext: ByteArray): ByteArray {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        val secretKey = SecretKeySpec(key, "AES")

        // GCM nonce is first 12 bytes of the 24-byte nonce
        val gcmNonce = ByteArray(GCM_NONCE_SIZE)
        SecureRandom().nextBytes(gcmNonce)

        val spec = GCMParameterSpec(GCM_TAG_SIZE, gcmNonce)
        cipher.init(Cipher.ENCRYPT_MODE, secretKey, spec)

        val ciphertext = cipher.doFinal(plaintext)

        // Return nonce + ciphertext
        return gcmNonce + ciphertext
    }

    fun decrypt(key: ByteArray, ciphertext: ByteArray): ByteArray {
        if (ciphertext.size < GCM_NONCE_SIZE) {
            throw IllegalArgumentException("Ciphertext too short")
        }

        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        val secretKey = SecretKeySpec(key, "AES")

        val gcmNonce = ciphertext.copyOfRange(0, GCM_NONCE_SIZE)
        val encryptedData = ciphertext.copyOfRange(GCM_NONCE_SIZE, ciphertext.size)

        val spec = GCMParameterSpec(GCM_TAG_SIZE, gcmNonce)
        cipher.init(Cipher.DECRYPT_MODE, secretKey, spec)

        return cipher.doFinal(encryptedData)
    }
}
