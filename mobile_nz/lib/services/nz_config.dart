import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';

class NzConfig {
  String name;
  String password;
  String domain;
  int ip;

  NzConfig({
    required this.name,
    required this.password,
    required this.domain,
    required this.ip,
  });

  bool get isValid =>
      name.isNotEmpty && password.isNotEmpty && domain.isNotEmpty && ip >= 2 && ip <= 254;

  Map<String, dynamic> toJson() => {
        'mode': 'node',
        'name': name,
        'password': password,
        'domain': domain,
        'ip': ip,
      };

  String toJsonString() => jsonEncode(toJson());

  factory NzConfig.fromJson(Map<String, dynamic> json) => NzConfig(
        name: json['name'] ?? '',
        password: json['password'] ?? '',
        domain: json['domain'] ?? '',
        ip: json['ip'] ?? 2,
      );

  static Future<NzConfig?> load() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString('nz_config');
    if (raw == null) return null;
    try {
      return NzConfig.fromJson(jsonDecode(raw));
    } catch (_) {
      return null;
    }
  }

  Future<void> save() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('nz_config', jsonEncode(toJson()));
  }

  Future<void> delete() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove('nz_config');
  }
}
