package dev.deli.devhud

import org.junit.Assert.assertEquals
import org.junit.Test

class NativeContractTest {
    @Test
    fun applicationIdentityIsStable() {
        assertEquals("dev.deli.devhud", BuildConfig.APPLICATION_ID)
    }
}
