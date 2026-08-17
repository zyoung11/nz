import 'package:flutter/services.dart';

class NzService {
  static const MethodChannel _channel = MethodChannel('nz_vpn');

  static Future<bool> start(String config) async {
    try {
      final result = await _channel.invokeMethod('start', {'config': config});
      return result == true;
    } catch (e) {
      return false;
    }
  }

  static Future<bool> stop() async {
    try {
      final result = await _channel.invokeMethod('stop');
      return result == true;
    } catch (e) {
      return false;
    }
  }

  static Future<bool> isRunning() async {
    try {
      final result = await _channel.invokeMethod('isRunning');
      return result == true;
    } catch (e) {
      return false;
    }
  }
}
