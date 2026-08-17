import 'dart:convert';
import 'dart:io';
import 'package:flutter/services.dart';

class NzNodeInfo {
  final String name;
  final String ip;
  final String status;

  NzNodeInfo({
    required this.name,
    required this.ip,
    required this.status,
  });

  factory NzNodeInfo.fromJson(Map<String, dynamic> json) {
    return NzNodeInfo(
      name: json['name'] ?? '',
      ip: json['ip'] ?? '',
      status: json['status'] ?? 'offline',
    );
  }
}

class NzLsService {
  static const MethodChannel _channel = MethodChannel('nz_vpn');

  static Future<List<NzNodeInfo>> getNodeList() async {
    try {
      print('NzLsService: calling getNodeList...');
      final result = await _channel.invokeMethod('getNodeList');
      print('NzLsService: result=$result');
      if (result == null) return [];
      
      final List<dynamic> list = jsonDecode(result);
      print('NzLsService: parsed ${list.length} nodes');
      return list.map((e) => NzNodeInfo.fromJson(e)).toList();
    } catch (e) {
      print('NzLsService: error=$e');
      return [];
    }
  }
}
