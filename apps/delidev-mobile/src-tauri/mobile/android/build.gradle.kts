plugins { id("com.android.library"); id("org.jetbrains.kotlin.android") }
android { namespace = "io.delino.delidev.mobile.bridge"; compileSdk = 36
 defaultConfig { minSdk = 31; consumerProguardFiles("consumer-rules.pro") }
 compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
}
dependencies { implementation(project(":tauri-android")) }
