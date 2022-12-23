/*eslint-disable block-scoped-var, id-length, no-control-regex, no-magic-numbers, no-prototype-builtins, no-redeclare, no-shadow, no-var, sort-vars*/
import * as $protobuf from "protobufjs/minimal";

// Common aliases
const $Reader = $protobuf.Reader, $Writer = $protobuf.Writer, $util = $protobuf.util;

// Exported root namespace
const $root = $protobuf.roots["default"] || ($protobuf.roots["default"] = {});

export const archiveteam_tracker = $root.archiveteam_tracker = (() => {

    /**
     * Namespace archiveteam_tracker.
     * @exports archiveteam_tracker
     * @namespace
     */
    const archiveteam_tracker = {};

    archiveteam_tracker.WarriorMeta = (function() {

        /**
         * Properties of a WarriorMeta.
         * @memberof archiveteam_tracker
         * @interface IWarriorMeta
         * @property {string|null} [userAgent] WarriorMeta userAgent
         * @property {string|null} [version] WarriorMeta version
         */

        /**
         * Constructs a new WarriorMeta.
         * @memberof archiveteam_tracker
         * @classdesc Represents a WarriorMeta.
         * @implements IWarriorMeta
         * @constructor
         * @param {archiveteam_tracker.IWarriorMeta=} [properties] Properties to set
         */
        function WarriorMeta(properties) {
            if (properties)
                for (let keys = Object.keys(properties), i = 0; i < keys.length; ++i)
                    if (properties[keys[i]] != null)
                        this[keys[i]] = properties[keys[i]];
        }

        /**
         * WarriorMeta userAgent.
         * @member {string} userAgent
         * @memberof archiveteam_tracker.WarriorMeta
         * @instance
         */
        WarriorMeta.prototype.userAgent = "";

        /**
         * WarriorMeta version.
         * @member {string} version
         * @memberof archiveteam_tracker.WarriorMeta
         * @instance
         */
        WarriorMeta.prototype.version = "";

        /**
         * Creates a new WarriorMeta instance using the specified properties.
         * @function create
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {archiveteam_tracker.IWarriorMeta=} [properties] Properties to set
         * @returns {archiveteam_tracker.WarriorMeta} WarriorMeta instance
         */
        WarriorMeta.create = function create(properties) {
            return new WarriorMeta(properties);
        };

        /**
         * Encodes the specified WarriorMeta message. Does not implicitly {@link archiveteam_tracker.WarriorMeta.verify|verify} messages.
         * @function encode
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {archiveteam_tracker.IWarriorMeta} message WarriorMeta message or plain object to encode
         * @param {$protobuf.Writer} [writer] Writer to encode to
         * @returns {$protobuf.Writer} Writer
         */
        WarriorMeta.encode = function encode(message, writer) {
            if (!writer)
                writer = $Writer.create();
            if (message.userAgent != null && Object.hasOwnProperty.call(message, "userAgent"))
                writer.uint32(/* id 1, wireType 2 =*/10).string(message.userAgent);
            if (message.version != null && Object.hasOwnProperty.call(message, "version"))
                writer.uint32(/* id 2, wireType 2 =*/18).string(message.version);
            return writer;
        };

        /**
         * Encodes the specified WarriorMeta message, length delimited. Does not implicitly {@link archiveteam_tracker.WarriorMeta.verify|verify} messages.
         * @function encodeDelimited
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {archiveteam_tracker.IWarriorMeta} message WarriorMeta message or plain object to encode
         * @param {$protobuf.Writer} [writer] Writer to encode to
         * @returns {$protobuf.Writer} Writer
         */
        WarriorMeta.encodeDelimited = function encodeDelimited(message, writer) {
            return this.encode(message, writer).ldelim();
        };

        /**
         * Decodes a WarriorMeta message from the specified reader or buffer.
         * @function decode
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {$protobuf.Reader|Uint8Array} reader Reader or buffer to decode from
         * @param {number} [length] Message length if known beforehand
         * @returns {archiveteam_tracker.WarriorMeta} WarriorMeta
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        WarriorMeta.decode = function decode(reader, length) {
            if (!(reader instanceof $Reader))
                reader = $Reader.create(reader);
            let end = length === undefined ? reader.len : reader.pos + length, message = new $root.archiveteam_tracker.WarriorMeta();
            while (reader.pos < end) {
                let tag = reader.uint32();
                switch (tag >>> 3) {
                case 1: {
                        message.userAgent = reader.string();
                        break;
                    }
                case 2: {
                        message.version = reader.string();
                        break;
                    }
                default:
                    reader.skipType(tag & 7);
                    break;
                }
            }
            return message;
        };

        /**
         * Decodes a WarriorMeta message from the specified reader or buffer, length delimited.
         * @function decodeDelimited
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {$protobuf.Reader|Uint8Array} reader Reader or buffer to decode from
         * @returns {archiveteam_tracker.WarriorMeta} WarriorMeta
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        WarriorMeta.decodeDelimited = function decodeDelimited(reader) {
            if (!(reader instanceof $Reader))
                reader = new $Reader(reader);
            return this.decode(reader, reader.uint32());
        };

        /**
         * Verifies a WarriorMeta message.
         * @function verify
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {Object.<string,*>} message Plain object to verify
         * @returns {string|null} `null` if valid, otherwise the reason why it is not
         */
        WarriorMeta.verify = function verify(message) {
            if (typeof message !== "object" || message === null)
                return "object expected";
            if (message.userAgent != null && message.hasOwnProperty("userAgent"))
                if (!$util.isString(message.userAgent))
                    return "userAgent: string expected";
            if (message.version != null && message.hasOwnProperty("version"))
                if (!$util.isString(message.version))
                    return "version: string expected";
            return null;
        };

        /**
         * Creates a WarriorMeta message from a plain object. Also converts values to their respective internal types.
         * @function fromObject
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {Object.<string,*>} object Plain object
         * @returns {archiveteam_tracker.WarriorMeta} WarriorMeta
         */
        WarriorMeta.fromObject = function fromObject(object) {
            if (object instanceof $root.archiveteam_tracker.WarriorMeta)
                return object;
            let message = new $root.archiveteam_tracker.WarriorMeta();
            if (object.userAgent != null)
                message.userAgent = String(object.userAgent);
            if (object.version != null)
                message.version = String(object.version);
            return message;
        };

        /**
         * Creates a plain object from a WarriorMeta message. Also converts values to other types if specified.
         * @function toObject
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {archiveteam_tracker.WarriorMeta} message WarriorMeta
         * @param {$protobuf.IConversionOptions} [options] Conversion options
         * @returns {Object.<string,*>} Plain object
         */
        WarriorMeta.toObject = function toObject(message, options) {
            if (!options)
                options = {};
            let object = {};
            if (options.defaults) {
                object.userAgent = "";
                object.version = "";
            }
            if (message.userAgent != null && message.hasOwnProperty("userAgent"))
                object.userAgent = message.userAgent;
            if (message.version != null && message.hasOwnProperty("version"))
                object.version = message.version;
            return object;
        };

        /**
         * Converts this WarriorMeta to JSON.
         * @function toJSON
         * @memberof archiveteam_tracker.WarriorMeta
         * @instance
         * @returns {Object.<string,*>} JSON object
         */
        WarriorMeta.prototype.toJSON = function toJSON() {
            return this.constructor.toObject(this, $protobuf.util.toJSONOptions);
        };

        /**
         * Gets the default type url for WarriorMeta
         * @function getTypeUrl
         * @memberof archiveteam_tracker.WarriorMeta
         * @static
         * @param {string} [typeUrlPrefix] your custom typeUrlPrefix(default "type.googleapis.com")
         * @returns {string} The default type url
         */
        WarriorMeta.getTypeUrl = function getTypeUrl(typeUrlPrefix) {
            if (typeUrlPrefix === undefined) {
                typeUrlPrefix = "type.googleapis.com";
            }
            return typeUrlPrefix + "/archiveteam_tracker.WarriorMeta";
        };

        return WarriorMeta;
    })();

    archiveteam_tracker.TrackerStats = (function() {

        /**
         * Properties of a TrackerStats.
         * @memberof archiveteam_tracker
         * @interface ITrackerStats
         * @property {Object.<string,number>|null} [values] TrackerStats values
         * @property {Object.<string,number>|null} [queues] TrackerStats queues
         */

        /**
         * Constructs a new TrackerStats.
         * @memberof archiveteam_tracker
         * @classdesc Represents a TrackerStats.
         * @implements ITrackerStats
         * @constructor
         * @param {archiveteam_tracker.ITrackerStats=} [properties] Properties to set
         */
        function TrackerStats(properties) {
            this.values = {};
            this.queues = {};
            if (properties)
                for (let keys = Object.keys(properties), i = 0; i < keys.length; ++i)
                    if (properties[keys[i]] != null)
                        this[keys[i]] = properties[keys[i]];
        }

        /**
         * TrackerStats values.
         * @member {Object.<string,number>} values
         * @memberof archiveteam_tracker.TrackerStats
         * @instance
         */
        TrackerStats.prototype.values = $util.emptyObject;

        /**
         * TrackerStats queues.
         * @member {Object.<string,number>} queues
         * @memberof archiveteam_tracker.TrackerStats
         * @instance
         */
        TrackerStats.prototype.queues = $util.emptyObject;

        /**
         * Creates a new TrackerStats instance using the specified properties.
         * @function create
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {archiveteam_tracker.ITrackerStats=} [properties] Properties to set
         * @returns {archiveteam_tracker.TrackerStats} TrackerStats instance
         */
        TrackerStats.create = function create(properties) {
            return new TrackerStats(properties);
        };

        /**
         * Encodes the specified TrackerStats message. Does not implicitly {@link archiveteam_tracker.TrackerStats.verify|verify} messages.
         * @function encode
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {archiveteam_tracker.ITrackerStats} message TrackerStats message or plain object to encode
         * @param {$protobuf.Writer} [writer] Writer to encode to
         * @returns {$protobuf.Writer} Writer
         */
        TrackerStats.encode = function encode(message, writer) {
            if (!writer)
                writer = $Writer.create();
            if (message.values != null && Object.hasOwnProperty.call(message, "values"))
                for (let keys = Object.keys(message.values), i = 0; i < keys.length; ++i)
                    writer.uint32(/* id 1, wireType 2 =*/10).fork().uint32(/* id 1, wireType 2 =*/10).string(keys[i]).uint32(/* id 2, wireType 1 =*/17).double(message.values[keys[i]]).ldelim();
            if (message.queues != null && Object.hasOwnProperty.call(message, "queues"))
                for (let keys = Object.keys(message.queues), i = 0; i < keys.length; ++i)
                    writer.uint32(/* id 2, wireType 2 =*/18).fork().uint32(/* id 1, wireType 2 =*/10).string(keys[i]).uint32(/* id 2, wireType 1 =*/17).double(message.queues[keys[i]]).ldelim();
            return writer;
        };

        /**
         * Encodes the specified TrackerStats message, length delimited. Does not implicitly {@link archiveteam_tracker.TrackerStats.verify|verify} messages.
         * @function encodeDelimited
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {archiveteam_tracker.ITrackerStats} message TrackerStats message or plain object to encode
         * @param {$protobuf.Writer} [writer] Writer to encode to
         * @returns {$protobuf.Writer} Writer
         */
        TrackerStats.encodeDelimited = function encodeDelimited(message, writer) {
            return this.encode(message, writer).ldelim();
        };

        /**
         * Decodes a TrackerStats message from the specified reader or buffer.
         * @function decode
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {$protobuf.Reader|Uint8Array} reader Reader or buffer to decode from
         * @param {number} [length] Message length if known beforehand
         * @returns {archiveteam_tracker.TrackerStats} TrackerStats
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        TrackerStats.decode = function decode(reader, length) {
            if (!(reader instanceof $Reader))
                reader = $Reader.create(reader);
            let end = length === undefined ? reader.len : reader.pos + length, message = new $root.archiveteam_tracker.TrackerStats(), key, value;
            while (reader.pos < end) {
                let tag = reader.uint32();
                switch (tag >>> 3) {
                case 1: {
                        if (message.values === $util.emptyObject)
                            message.values = {};
                        let end2 = reader.uint32() + reader.pos;
                        key = "";
                        value = 0;
                        while (reader.pos < end2) {
                            let tag2 = reader.uint32();
                            switch (tag2 >>> 3) {
                            case 1:
                                key = reader.string();
                                break;
                            case 2:
                                value = reader.double();
                                break;
                            default:
                                reader.skipType(tag2 & 7);
                                break;
                            }
                        }
                        message.values[key] = value;
                        break;
                    }
                case 2: {
                        if (message.queues === $util.emptyObject)
                            message.queues = {};
                        let end2 = reader.uint32() + reader.pos;
                        key = "";
                        value = 0;
                        while (reader.pos < end2) {
                            let tag2 = reader.uint32();
                            switch (tag2 >>> 3) {
                            case 1:
                                key = reader.string();
                                break;
                            case 2:
                                value = reader.double();
                                break;
                            default:
                                reader.skipType(tag2 & 7);
                                break;
                            }
                        }
                        message.queues[key] = value;
                        break;
                    }
                default:
                    reader.skipType(tag & 7);
                    break;
                }
            }
            return message;
        };

        /**
         * Decodes a TrackerStats message from the specified reader or buffer, length delimited.
         * @function decodeDelimited
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {$protobuf.Reader|Uint8Array} reader Reader or buffer to decode from
         * @returns {archiveteam_tracker.TrackerStats} TrackerStats
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        TrackerStats.decodeDelimited = function decodeDelimited(reader) {
            if (!(reader instanceof $Reader))
                reader = new $Reader(reader);
            return this.decode(reader, reader.uint32());
        };

        /**
         * Verifies a TrackerStats message.
         * @function verify
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {Object.<string,*>} message Plain object to verify
         * @returns {string|null} `null` if valid, otherwise the reason why it is not
         */
        TrackerStats.verify = function verify(message) {
            if (typeof message !== "object" || message === null)
                return "object expected";
            if (message.values != null && message.hasOwnProperty("values")) {
                if (!$util.isObject(message.values))
                    return "values: object expected";
                let key = Object.keys(message.values);
                for (let i = 0; i < key.length; ++i)
                    if (typeof message.values[key[i]] !== "number")
                        return "values: number{k:string} expected";
            }
            if (message.queues != null && message.hasOwnProperty("queues")) {
                if (!$util.isObject(message.queues))
                    return "queues: object expected";
                let key = Object.keys(message.queues);
                for (let i = 0; i < key.length; ++i)
                    if (typeof message.queues[key[i]] !== "number")
                        return "queues: number{k:string} expected";
            }
            return null;
        };

        /**
         * Creates a TrackerStats message from a plain object. Also converts values to their respective internal types.
         * @function fromObject
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {Object.<string,*>} object Plain object
         * @returns {archiveteam_tracker.TrackerStats} TrackerStats
         */
        TrackerStats.fromObject = function fromObject(object) {
            if (object instanceof $root.archiveteam_tracker.TrackerStats)
                return object;
            let message = new $root.archiveteam_tracker.TrackerStats();
            if (object.values) {
                if (typeof object.values !== "object")
                    throw TypeError(".archiveteam_tracker.TrackerStats.values: object expected");
                message.values = {};
                for (let keys = Object.keys(object.values), i = 0; i < keys.length; ++i)
                    message.values[keys[i]] = Number(object.values[keys[i]]);
            }
            if (object.queues) {
                if (typeof object.queues !== "object")
                    throw TypeError(".archiveteam_tracker.TrackerStats.queues: object expected");
                message.queues = {};
                for (let keys = Object.keys(object.queues), i = 0; i < keys.length; ++i)
                    message.queues[keys[i]] = Number(object.queues[keys[i]]);
            }
            return message;
        };

        /**
         * Creates a plain object from a TrackerStats message. Also converts values to other types if specified.
         * @function toObject
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {archiveteam_tracker.TrackerStats} message TrackerStats
         * @param {$protobuf.IConversionOptions} [options] Conversion options
         * @returns {Object.<string,*>} Plain object
         */
        TrackerStats.toObject = function toObject(message, options) {
            if (!options)
                options = {};
            let object = {};
            if (options.objects || options.defaults) {
                object.values = {};
                object.queues = {};
            }
            let keys2;
            if (message.values && (keys2 = Object.keys(message.values)).length) {
                object.values = {};
                for (let j = 0; j < keys2.length; ++j)
                    object.values[keys2[j]] = options.json && !isFinite(message.values[keys2[j]]) ? String(message.values[keys2[j]]) : message.values[keys2[j]];
            }
            if (message.queues && (keys2 = Object.keys(message.queues)).length) {
                object.queues = {};
                for (let j = 0; j < keys2.length; ++j)
                    object.queues[keys2[j]] = options.json && !isFinite(message.queues[keys2[j]]) ? String(message.queues[keys2[j]]) : message.queues[keys2[j]];
            }
            return object;
        };

        /**
         * Converts this TrackerStats to JSON.
         * @function toJSON
         * @memberof archiveteam_tracker.TrackerStats
         * @instance
         * @returns {Object.<string,*>} JSON object
         */
        TrackerStats.prototype.toJSON = function toJSON() {
            return this.constructor.toObject(this, $protobuf.util.toJSONOptions);
        };

        /**
         * Gets the default type url for TrackerStats
         * @function getTypeUrl
         * @memberof archiveteam_tracker.TrackerStats
         * @static
         * @param {string} [typeUrlPrefix] your custom typeUrlPrefix(default "type.googleapis.com")
         * @returns {string} The default type url
         */
        TrackerStats.getTypeUrl = function getTypeUrl(typeUrlPrefix) {
            if (typeUrlPrefix === undefined) {
                typeUrlPrefix = "type.googleapis.com";
            }
            return typeUrlPrefix + "/archiveteam_tracker.TrackerStats";
        };

        return TrackerStats;
    })();

    archiveteam_tracker.TrackerEvent = (function() {

        /**
         * Properties of a TrackerEvent.
         * @memberof archiveteam_tracker
         * @interface ITrackerEvent
         * @property {string|null} [project] TrackerEvent project
         * @property {string|null} [downloader] TrackerEvent downloader
         * @property {string|null} [logChannel] TrackerEvent logChannel
         * @property {number|null} [timestamp] TrackerEvent timestamp
         * @property {number|Long|null} [bytes] TrackerEvent bytes
         * @property {number|null} [sizeMb] TrackerEvent sizeMb
         * @property {boolean|null} [valid] TrackerEvent valid
         * @property {boolean|null} [isDuplicate] TrackerEvent isDuplicate
         * @property {string|null} [item] TrackerEvent item
         * @property {Array.<string>|null} [items] TrackerEvent items
         * @property {Array.<string>|null} [moveItems] TrackerEvent moveItems
         * @property {Array.<number>|null} [itemRtts] TrackerEvent itemRtts
         * @property {string|null} [WarriorUserAgent] TrackerEvent WarriorUserAgent
         * @property {string|null} [WarriorVersion] TrackerEvent WarriorVersion
         * @property {Object.<string,number>|null} [queueStats] TrackerEvent queueStats
         * @property {Object.<string,number>|null} [counts] TrackerEvent counts
         * @property {archiveteam_tracker.ITrackerStats|null} [trackerStats] TrackerEvent trackerStats
         * @property {Object.<string,number|Long>|null} [domainBytes] TrackerEvent domainBytes
         */

        /**
         * Constructs a new TrackerEvent.
         * @memberof archiveteam_tracker
         * @classdesc Represents a TrackerEvent.
         * @implements ITrackerEvent
         * @constructor
         * @param {archiveteam_tracker.ITrackerEvent=} [properties] Properties to set
         */
        function TrackerEvent(properties) {
            this.items = [];
            this.moveItems = [];
            this.itemRtts = [];
            this.queueStats = {};
            this.counts = {};
            this.domainBytes = {};
            if (properties)
                for (let keys = Object.keys(properties), i = 0; i < keys.length; ++i)
                    if (properties[keys[i]] != null)
                        this[keys[i]] = properties[keys[i]];
        }

        /**
         * TrackerEvent project.
         * @member {string} project
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.project = "";

        /**
         * TrackerEvent downloader.
         * @member {string} downloader
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.downloader = "";

        /**
         * TrackerEvent logChannel.
         * @member {string} logChannel
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.logChannel = "";

        /**
         * TrackerEvent timestamp.
         * @member {number} timestamp
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.timestamp = 0;

        /**
         * TrackerEvent bytes.
         * @member {number|Long} bytes
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.bytes = $util.Long ? $util.Long.fromBits(0,0,true) : 0;

        /**
         * TrackerEvent sizeMb.
         * @member {number} sizeMb
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.sizeMb = 0;

        /**
         * TrackerEvent valid.
         * @member {boolean} valid
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.valid = false;

        /**
         * TrackerEvent isDuplicate.
         * @member {boolean} isDuplicate
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.isDuplicate = false;

        /**
         * TrackerEvent item.
         * @member {string} item
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.item = "";

        /**
         * TrackerEvent items.
         * @member {Array.<string>} items
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.items = $util.emptyArray;

        /**
         * TrackerEvent moveItems.
         * @member {Array.<string>} moveItems
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.moveItems = $util.emptyArray;

        /**
         * TrackerEvent itemRtts.
         * @member {Array.<number>} itemRtts
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.itemRtts = $util.emptyArray;

        /**
         * TrackerEvent WarriorUserAgent.
         * @member {string} WarriorUserAgent
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.WarriorUserAgent = "";

        /**
         * TrackerEvent WarriorVersion.
         * @member {string} WarriorVersion
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.WarriorVersion = "";

        /**
         * TrackerEvent queueStats.
         * @member {Object.<string,number>} queueStats
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.queueStats = $util.emptyObject;

        /**
         * TrackerEvent counts.
         * @member {Object.<string,number>} counts
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.counts = $util.emptyObject;

        /**
         * TrackerEvent trackerStats.
         * @member {archiveteam_tracker.ITrackerStats|null|undefined} trackerStats
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.trackerStats = null;

        /**
         * TrackerEvent domainBytes.
         * @member {Object.<string,number|Long>} domainBytes
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         */
        TrackerEvent.prototype.domainBytes = $util.emptyObject;

        /**
         * Creates a new TrackerEvent instance using the specified properties.
         * @function create
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {archiveteam_tracker.ITrackerEvent=} [properties] Properties to set
         * @returns {archiveteam_tracker.TrackerEvent} TrackerEvent instance
         */
        TrackerEvent.create = function create(properties) {
            return new TrackerEvent(properties);
        };

        /**
         * Encodes the specified TrackerEvent message. Does not implicitly {@link archiveteam_tracker.TrackerEvent.verify|verify} messages.
         * @function encode
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {archiveteam_tracker.ITrackerEvent} message TrackerEvent message or plain object to encode
         * @param {$protobuf.Writer} [writer] Writer to encode to
         * @returns {$protobuf.Writer} Writer
         */
        TrackerEvent.encode = function encode(message, writer) {
            if (!writer)
                writer = $Writer.create();
            if (message.project != null && Object.hasOwnProperty.call(message, "project"))
                writer.uint32(/* id 1, wireType 2 =*/10).string(message.project);
            if (message.downloader != null && Object.hasOwnProperty.call(message, "downloader"))
                writer.uint32(/* id 2, wireType 2 =*/18).string(message.downloader);
            if (message.logChannel != null && Object.hasOwnProperty.call(message, "logChannel"))
                writer.uint32(/* id 3, wireType 2 =*/26).string(message.logChannel);
            if (message.timestamp != null && Object.hasOwnProperty.call(message, "timestamp"))
                writer.uint32(/* id 4, wireType 1 =*/33).double(message.timestamp);
            if (message.bytes != null && Object.hasOwnProperty.call(message, "bytes"))
                writer.uint32(/* id 10, wireType 0 =*/80).uint64(message.bytes);
            if (message.sizeMb != null && Object.hasOwnProperty.call(message, "sizeMb"))
                writer.uint32(/* id 11, wireType 5 =*/93).float(message.sizeMb);
            if (message.valid != null && Object.hasOwnProperty.call(message, "valid"))
                writer.uint32(/* id 12, wireType 0 =*/96).bool(message.valid);
            if (message.isDuplicate != null && Object.hasOwnProperty.call(message, "isDuplicate"))
                writer.uint32(/* id 13, wireType 0 =*/104).bool(message.isDuplicate);
            if (message.item != null && Object.hasOwnProperty.call(message, "item"))
                writer.uint32(/* id 20, wireType 2 =*/162).string(message.item);
            if (message.items != null && message.items.length)
                for (let i = 0; i < message.items.length; ++i)
                    writer.uint32(/* id 21, wireType 2 =*/170).string(message.items[i]);
            if (message.moveItems != null && message.moveItems.length)
                for (let i = 0; i < message.moveItems.length; ++i)
                    writer.uint32(/* id 22, wireType 2 =*/178).string(message.moveItems[i]);
            if (message.itemRtts != null && message.itemRtts.length) {
                writer.uint32(/* id 23, wireType 2 =*/186).fork();
                for (let i = 0; i < message.itemRtts.length; ++i)
                    writer.double(message.itemRtts[i]);
                writer.ldelim();
            }
            if (message.WarriorUserAgent != null && Object.hasOwnProperty.call(message, "WarriorUserAgent"))
                writer.uint32(/* id 30, wireType 2 =*/242).string(message.WarriorUserAgent);
            if (message.WarriorVersion != null && Object.hasOwnProperty.call(message, "WarriorVersion"))
                writer.uint32(/* id 31, wireType 2 =*/250).string(message.WarriorVersion);
            if (message.queueStats != null && Object.hasOwnProperty.call(message, "queueStats"))
                for (let keys = Object.keys(message.queueStats), i = 0; i < keys.length; ++i)
                    writer.uint32(/* id 40, wireType 2 =*/322).fork().uint32(/* id 1, wireType 2 =*/10).string(keys[i]).uint32(/* id 2, wireType 1 =*/17).double(message.queueStats[keys[i]]).ldelim();
            if (message.counts != null && Object.hasOwnProperty.call(message, "counts"))
                for (let keys = Object.keys(message.counts), i = 0; i < keys.length; ++i)
                    writer.uint32(/* id 41, wireType 2 =*/330).fork().uint32(/* id 1, wireType 2 =*/10).string(keys[i]).uint32(/* id 2, wireType 1 =*/17).double(message.counts[keys[i]]).ldelim();
            if (message.trackerStats != null && Object.hasOwnProperty.call(message, "trackerStats"))
                $root.archiveteam_tracker.TrackerStats.encode(message.trackerStats, writer.uint32(/* id 42, wireType 2 =*/338).fork()).ldelim();
            if (message.domainBytes != null && Object.hasOwnProperty.call(message, "domainBytes"))
                for (let keys = Object.keys(message.domainBytes), i = 0; i < keys.length; ++i)
                    writer.uint32(/* id 43, wireType 2 =*/346).fork().uint32(/* id 1, wireType 2 =*/10).string(keys[i]).uint32(/* id 2, wireType 0 =*/16).uint64(message.domainBytes[keys[i]]).ldelim();
            return writer;
        };

        /**
         * Encodes the specified TrackerEvent message, length delimited. Does not implicitly {@link archiveteam_tracker.TrackerEvent.verify|verify} messages.
         * @function encodeDelimited
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {archiveteam_tracker.ITrackerEvent} message TrackerEvent message or plain object to encode
         * @param {$protobuf.Writer} [writer] Writer to encode to
         * @returns {$protobuf.Writer} Writer
         */
        TrackerEvent.encodeDelimited = function encodeDelimited(message, writer) {
            return this.encode(message, writer).ldelim();
        };

        /**
         * Decodes a TrackerEvent message from the specified reader or buffer.
         * @function decode
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {$protobuf.Reader|Uint8Array} reader Reader or buffer to decode from
         * @param {number} [length] Message length if known beforehand
         * @returns {archiveteam_tracker.TrackerEvent} TrackerEvent
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        TrackerEvent.decode = function decode(reader, length) {
            if (!(reader instanceof $Reader))
                reader = $Reader.create(reader);
            let end = length === undefined ? reader.len : reader.pos + length, message = new $root.archiveteam_tracker.TrackerEvent(), key, value;
            while (reader.pos < end) {
                let tag = reader.uint32();
                switch (tag >>> 3) {
                case 1: {
                        message.project = reader.string();
                        break;
                    }
                case 2: {
                        message.downloader = reader.string();
                        break;
                    }
                case 3: {
                        message.logChannel = reader.string();
                        break;
                    }
                case 4: {
                        message.timestamp = reader.double();
                        break;
                    }
                case 10: {
                        message.bytes = reader.uint64();
                        break;
                    }
                case 11: {
                        message.sizeMb = reader.float();
                        break;
                    }
                case 12: {
                        message.valid = reader.bool();
                        break;
                    }
                case 13: {
                        message.isDuplicate = reader.bool();
                        break;
                    }
                case 20: {
                        message.item = reader.string();
                        break;
                    }
                case 21: {
                        if (!(message.items && message.items.length))
                            message.items = [];
                        message.items.push(reader.string());
                        break;
                    }
                case 22: {
                        if (!(message.moveItems && message.moveItems.length))
                            message.moveItems = [];
                        message.moveItems.push(reader.string());
                        break;
                    }
                case 23: {
                        if (!(message.itemRtts && message.itemRtts.length))
                            message.itemRtts = [];
                        if ((tag & 7) === 2) {
                            let end2 = reader.uint32() + reader.pos;
                            while (reader.pos < end2)
                                message.itemRtts.push(reader.double());
                        } else
                            message.itemRtts.push(reader.double());
                        break;
                    }
                case 30: {
                        message.WarriorUserAgent = reader.string();
                        break;
                    }
                case 31: {
                        message.WarriorVersion = reader.string();
                        break;
                    }
                case 40: {
                        if (message.queueStats === $util.emptyObject)
                            message.queueStats = {};
                        let end2 = reader.uint32() + reader.pos;
                        key = "";
                        value = 0;
                        while (reader.pos < end2) {
                            let tag2 = reader.uint32();
                            switch (tag2 >>> 3) {
                            case 1:
                                key = reader.string();
                                break;
                            case 2:
                                value = reader.double();
                                break;
                            default:
                                reader.skipType(tag2 & 7);
                                break;
                            }
                        }
                        message.queueStats[key] = value;
                        break;
                    }
                case 41: {
                        if (message.counts === $util.emptyObject)
                            message.counts = {};
                        let end2 = reader.uint32() + reader.pos;
                        key = "";
                        value = 0;
                        while (reader.pos < end2) {
                            let tag2 = reader.uint32();
                            switch (tag2 >>> 3) {
                            case 1:
                                key = reader.string();
                                break;
                            case 2:
                                value = reader.double();
                                break;
                            default:
                                reader.skipType(tag2 & 7);
                                break;
                            }
                        }
                        message.counts[key] = value;
                        break;
                    }
                case 42: {
                        message.trackerStats = $root.archiveteam_tracker.TrackerStats.decode(reader, reader.uint32());
                        break;
                    }
                case 43: {
                        if (message.domainBytes === $util.emptyObject)
                            message.domainBytes = {};
                        let end2 = reader.uint32() + reader.pos;
                        key = "";
                        value = 0;
                        while (reader.pos < end2) {
                            let tag2 = reader.uint32();
                            switch (tag2 >>> 3) {
                            case 1:
                                key = reader.string();
                                break;
                            case 2:
                                value = reader.uint64();
                                break;
                            default:
                                reader.skipType(tag2 & 7);
                                break;
                            }
                        }
                        message.domainBytes[key] = value;
                        break;
                    }
                default:
                    reader.skipType(tag & 7);
                    break;
                }
            }
            return message;
        };

        /**
         * Decodes a TrackerEvent message from the specified reader or buffer, length delimited.
         * @function decodeDelimited
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {$protobuf.Reader|Uint8Array} reader Reader or buffer to decode from
         * @returns {archiveteam_tracker.TrackerEvent} TrackerEvent
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        TrackerEvent.decodeDelimited = function decodeDelimited(reader) {
            if (!(reader instanceof $Reader))
                reader = new $Reader(reader);
            return this.decode(reader, reader.uint32());
        };

        /**
         * Verifies a TrackerEvent message.
         * @function verify
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {Object.<string,*>} message Plain object to verify
         * @returns {string|null} `null` if valid, otherwise the reason why it is not
         */
        TrackerEvent.verify = function verify(message) {
            if (typeof message !== "object" || message === null)
                return "object expected";
            if (message.project != null && message.hasOwnProperty("project"))
                if (!$util.isString(message.project))
                    return "project: string expected";
            if (message.downloader != null && message.hasOwnProperty("downloader"))
                if (!$util.isString(message.downloader))
                    return "downloader: string expected";
            if (message.logChannel != null && message.hasOwnProperty("logChannel"))
                if (!$util.isString(message.logChannel))
                    return "logChannel: string expected";
            if (message.timestamp != null && message.hasOwnProperty("timestamp"))
                if (typeof message.timestamp !== "number")
                    return "timestamp: number expected";
            if (message.bytes != null && message.hasOwnProperty("bytes"))
                if (!$util.isInteger(message.bytes) && !(message.bytes && $util.isInteger(message.bytes.low) && $util.isInteger(message.bytes.high)))
                    return "bytes: integer|Long expected";
            if (message.sizeMb != null && message.hasOwnProperty("sizeMb"))
                if (typeof message.sizeMb !== "number")
                    return "sizeMb: number expected";
            if (message.valid != null && message.hasOwnProperty("valid"))
                if (typeof message.valid !== "boolean")
                    return "valid: boolean expected";
            if (message.isDuplicate != null && message.hasOwnProperty("isDuplicate"))
                if (typeof message.isDuplicate !== "boolean")
                    return "isDuplicate: boolean expected";
            if (message.item != null && message.hasOwnProperty("item"))
                if (!$util.isString(message.item))
                    return "item: string expected";
            if (message.items != null && message.hasOwnProperty("items")) {
                if (!Array.isArray(message.items))
                    return "items: array expected";
                for (let i = 0; i < message.items.length; ++i)
                    if (!$util.isString(message.items[i]))
                        return "items: string[] expected";
            }
            if (message.moveItems != null && message.hasOwnProperty("moveItems")) {
                if (!Array.isArray(message.moveItems))
                    return "moveItems: array expected";
                for (let i = 0; i < message.moveItems.length; ++i)
                    if (!$util.isString(message.moveItems[i]))
                        return "moveItems: string[] expected";
            }
            if (message.itemRtts != null && message.hasOwnProperty("itemRtts")) {
                if (!Array.isArray(message.itemRtts))
                    return "itemRtts: array expected";
                for (let i = 0; i < message.itemRtts.length; ++i)
                    if (typeof message.itemRtts[i] !== "number")
                        return "itemRtts: number[] expected";
            }
            if (message.WarriorUserAgent != null && message.hasOwnProperty("WarriorUserAgent"))
                if (!$util.isString(message.WarriorUserAgent))
                    return "WarriorUserAgent: string expected";
            if (message.WarriorVersion != null && message.hasOwnProperty("WarriorVersion"))
                if (!$util.isString(message.WarriorVersion))
                    return "WarriorVersion: string expected";
            if (message.queueStats != null && message.hasOwnProperty("queueStats")) {
                if (!$util.isObject(message.queueStats))
                    return "queueStats: object expected";
                let key = Object.keys(message.queueStats);
                for (let i = 0; i < key.length; ++i)
                    if (typeof message.queueStats[key[i]] !== "number")
                        return "queueStats: number{k:string} expected";
            }
            if (message.counts != null && message.hasOwnProperty("counts")) {
                if (!$util.isObject(message.counts))
                    return "counts: object expected";
                let key = Object.keys(message.counts);
                for (let i = 0; i < key.length; ++i)
                    if (typeof message.counts[key[i]] !== "number")
                        return "counts: number{k:string} expected";
            }
            if (message.trackerStats != null && message.hasOwnProperty("trackerStats")) {
                let error = $root.archiveteam_tracker.TrackerStats.verify(message.trackerStats);
                if (error)
                    return "trackerStats." + error;
            }
            if (message.domainBytes != null && message.hasOwnProperty("domainBytes")) {
                if (!$util.isObject(message.domainBytes))
                    return "domainBytes: object expected";
                let key = Object.keys(message.domainBytes);
                for (let i = 0; i < key.length; ++i)
                    if (!$util.isInteger(message.domainBytes[key[i]]) && !(message.domainBytes[key[i]] && $util.isInteger(message.domainBytes[key[i]].low) && $util.isInteger(message.domainBytes[key[i]].high)))
                        return "domainBytes: integer|Long{k:string} expected";
            }
            return null;
        };

        /**
         * Creates a TrackerEvent message from a plain object. Also converts values to their respective internal types.
         * @function fromObject
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {Object.<string,*>} object Plain object
         * @returns {archiveteam_tracker.TrackerEvent} TrackerEvent
         */
        TrackerEvent.fromObject = function fromObject(object) {
            if (object instanceof $root.archiveteam_tracker.TrackerEvent)
                return object;
            let message = new $root.archiveteam_tracker.TrackerEvent();
            if (object.project != null)
                message.project = String(object.project);
            if (object.downloader != null)
                message.downloader = String(object.downloader);
            if (object.logChannel != null)
                message.logChannel = String(object.logChannel);
            if (object.timestamp != null)
                message.timestamp = Number(object.timestamp);
            if (object.bytes != null)
                if ($util.Long)
                    (message.bytes = $util.Long.fromValue(object.bytes)).unsigned = true;
                else if (typeof object.bytes === "string")
                    message.bytes = parseInt(object.bytes, 10);
                else if (typeof object.bytes === "number")
                    message.bytes = object.bytes;
                else if (typeof object.bytes === "object")
                    message.bytes = new $util.LongBits(object.bytes.low >>> 0, object.bytes.high >>> 0).toNumber(true);
            if (object.sizeMb != null)
                message.sizeMb = Number(object.sizeMb);
            if (object.valid != null)
                message.valid = Boolean(object.valid);
            if (object.isDuplicate != null)
                message.isDuplicate = Boolean(object.isDuplicate);
            if (object.item != null)
                message.item = String(object.item);
            if (object.items) {
                if (!Array.isArray(object.items))
                    throw TypeError(".archiveteam_tracker.TrackerEvent.items: array expected");
                message.items = [];
                for (let i = 0; i < object.items.length; ++i)
                    message.items[i] = String(object.items[i]);
            }
            if (object.moveItems) {
                if (!Array.isArray(object.moveItems))
                    throw TypeError(".archiveteam_tracker.TrackerEvent.moveItems: array expected");
                message.moveItems = [];
                for (let i = 0; i < object.moveItems.length; ++i)
                    message.moveItems[i] = String(object.moveItems[i]);
            }
            if (object.itemRtts) {
                if (!Array.isArray(object.itemRtts))
                    throw TypeError(".archiveteam_tracker.TrackerEvent.itemRtts: array expected");
                message.itemRtts = [];
                for (let i = 0; i < object.itemRtts.length; ++i)
                    message.itemRtts[i] = Number(object.itemRtts[i]);
            }
            if (object.WarriorUserAgent != null)
                message.WarriorUserAgent = String(object.WarriorUserAgent);
            if (object.WarriorVersion != null)
                message.WarriorVersion = String(object.WarriorVersion);
            if (object.queueStats) {
                if (typeof object.queueStats !== "object")
                    throw TypeError(".archiveteam_tracker.TrackerEvent.queueStats: object expected");
                message.queueStats = {};
                for (let keys = Object.keys(object.queueStats), i = 0; i < keys.length; ++i)
                    message.queueStats[keys[i]] = Number(object.queueStats[keys[i]]);
            }
            if (object.counts) {
                if (typeof object.counts !== "object")
                    throw TypeError(".archiveteam_tracker.TrackerEvent.counts: object expected");
                message.counts = {};
                for (let keys = Object.keys(object.counts), i = 0; i < keys.length; ++i)
                    message.counts[keys[i]] = Number(object.counts[keys[i]]);
            }
            if (object.trackerStats != null) {
                if (typeof object.trackerStats !== "object")
                    throw TypeError(".archiveteam_tracker.TrackerEvent.trackerStats: object expected");
                message.trackerStats = $root.archiveteam_tracker.TrackerStats.fromObject(object.trackerStats);
            }
            if (object.domainBytes) {
                if (typeof object.domainBytes !== "object")
                    throw TypeError(".archiveteam_tracker.TrackerEvent.domainBytes: object expected");
                message.domainBytes = {};
                for (let keys = Object.keys(object.domainBytes), i = 0; i < keys.length; ++i)
                    if ($util.Long)
                        (message.domainBytes[keys[i]] = $util.Long.fromValue(object.domainBytes[keys[i]])).unsigned = true;
                    else if (typeof object.domainBytes[keys[i]] === "string")
                        message.domainBytes[keys[i]] = parseInt(object.domainBytes[keys[i]], 10);
                    else if (typeof object.domainBytes[keys[i]] === "number")
                        message.domainBytes[keys[i]] = object.domainBytes[keys[i]];
                    else if (typeof object.domainBytes[keys[i]] === "object")
                        message.domainBytes[keys[i]] = new $util.LongBits(object.domainBytes[keys[i]].low >>> 0, object.domainBytes[keys[i]].high >>> 0).toNumber(true);
            }
            return message;
        };

        /**
         * Creates a plain object from a TrackerEvent message. Also converts values to other types if specified.
         * @function toObject
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {archiveteam_tracker.TrackerEvent} message TrackerEvent
         * @param {$protobuf.IConversionOptions} [options] Conversion options
         * @returns {Object.<string,*>} Plain object
         */
        TrackerEvent.toObject = function toObject(message, options) {
            if (!options)
                options = {};
            let object = {};
            if (options.arrays || options.defaults) {
                object.items = [];
                object.moveItems = [];
                object.itemRtts = [];
            }
            if (options.objects || options.defaults) {
                object.queueStats = {};
                object.counts = {};
                object.domainBytes = {};
            }
            if (options.defaults) {
                object.project = "";
                object.downloader = "";
                object.logChannel = "";
                object.timestamp = 0;
                if ($util.Long) {
                    let long = new $util.Long(0, 0, true);
                    object.bytes = options.longs === String ? long.toString() : options.longs === Number ? long.toNumber() : long;
                } else
                    object.bytes = options.longs === String ? "0" : 0;
                object.sizeMb = 0;
                object.valid = false;
                object.isDuplicate = false;
                object.item = "";
                object.WarriorUserAgent = "";
                object.WarriorVersion = "";
                object.trackerStats = null;
            }
            if (message.project != null && message.hasOwnProperty("project"))
                object.project = message.project;
            if (message.downloader != null && message.hasOwnProperty("downloader"))
                object.downloader = message.downloader;
            if (message.logChannel != null && message.hasOwnProperty("logChannel"))
                object.logChannel = message.logChannel;
            if (message.timestamp != null && message.hasOwnProperty("timestamp"))
                object.timestamp = options.json && !isFinite(message.timestamp) ? String(message.timestamp) : message.timestamp;
            if (message.bytes != null && message.hasOwnProperty("bytes"))
                if (typeof message.bytes === "number")
                    object.bytes = options.longs === String ? String(message.bytes) : message.bytes;
                else
                    object.bytes = options.longs === String ? $util.Long.prototype.toString.call(message.bytes) : options.longs === Number ? new $util.LongBits(message.bytes.low >>> 0, message.bytes.high >>> 0).toNumber(true) : message.bytes;
            if (message.sizeMb != null && message.hasOwnProperty("sizeMb"))
                object.sizeMb = options.json && !isFinite(message.sizeMb) ? String(message.sizeMb) : message.sizeMb;
            if (message.valid != null && message.hasOwnProperty("valid"))
                object.valid = message.valid;
            if (message.isDuplicate != null && message.hasOwnProperty("isDuplicate"))
                object.isDuplicate = message.isDuplicate;
            if (message.item != null && message.hasOwnProperty("item"))
                object.item = message.item;
            if (message.items && message.items.length) {
                object.items = [];
                for (let j = 0; j < message.items.length; ++j)
                    object.items[j] = message.items[j];
            }
            if (message.moveItems && message.moveItems.length) {
                object.moveItems = [];
                for (let j = 0; j < message.moveItems.length; ++j)
                    object.moveItems[j] = message.moveItems[j];
            }
            if (message.itemRtts && message.itemRtts.length) {
                object.itemRtts = [];
                for (let j = 0; j < message.itemRtts.length; ++j)
                    object.itemRtts[j] = options.json && !isFinite(message.itemRtts[j]) ? String(message.itemRtts[j]) : message.itemRtts[j];
            }
            if (message.WarriorUserAgent != null && message.hasOwnProperty("WarriorUserAgent"))
                object.WarriorUserAgent = message.WarriorUserAgent;
            if (message.WarriorVersion != null && message.hasOwnProperty("WarriorVersion"))
                object.WarriorVersion = message.WarriorVersion;
            let keys2;
            if (message.queueStats && (keys2 = Object.keys(message.queueStats)).length) {
                object.queueStats = {};
                for (let j = 0; j < keys2.length; ++j)
                    object.queueStats[keys2[j]] = options.json && !isFinite(message.queueStats[keys2[j]]) ? String(message.queueStats[keys2[j]]) : message.queueStats[keys2[j]];
            }
            if (message.counts && (keys2 = Object.keys(message.counts)).length) {
                object.counts = {};
                for (let j = 0; j < keys2.length; ++j)
                    object.counts[keys2[j]] = options.json && !isFinite(message.counts[keys2[j]]) ? String(message.counts[keys2[j]]) : message.counts[keys2[j]];
            }
            if (message.trackerStats != null && message.hasOwnProperty("trackerStats"))
                object.trackerStats = $root.archiveteam_tracker.TrackerStats.toObject(message.trackerStats, options);
            if (message.domainBytes && (keys2 = Object.keys(message.domainBytes)).length) {
                object.domainBytes = {};
                for (let j = 0; j < keys2.length; ++j)
                    if (typeof message.domainBytes[keys2[j]] === "number")
                        object.domainBytes[keys2[j]] = options.longs === String ? String(message.domainBytes[keys2[j]]) : message.domainBytes[keys2[j]];
                    else
                        object.domainBytes[keys2[j]] = options.longs === String ? $util.Long.prototype.toString.call(message.domainBytes[keys2[j]]) : options.longs === Number ? new $util.LongBits(message.domainBytes[keys2[j]].low >>> 0, message.domainBytes[keys2[j]].high >>> 0).toNumber(true) : message.domainBytes[keys2[j]];
            }
            return object;
        };

        /**
         * Converts this TrackerEvent to JSON.
         * @function toJSON
         * @memberof archiveteam_tracker.TrackerEvent
         * @instance
         * @returns {Object.<string,*>} JSON object
         */
        TrackerEvent.prototype.toJSON = function toJSON() {
            return this.constructor.toObject(this, $protobuf.util.toJSONOptions);
        };

        /**
         * Gets the default type url for TrackerEvent
         * @function getTypeUrl
         * @memberof archiveteam_tracker.TrackerEvent
         * @static
         * @param {string} [typeUrlPrefix] your custom typeUrlPrefix(default "type.googleapis.com")
         * @returns {string} The default type url
         */
        TrackerEvent.getTypeUrl = function getTypeUrl(typeUrlPrefix) {
            if (typeUrlPrefix === undefined) {
                typeUrlPrefix = "type.googleapis.com";
            }
            return typeUrlPrefix + "/archiveteam_tracker.TrackerEvent";
        };

        return TrackerEvent;
    })();

    return archiveteam_tracker;
})();

export { $root as default };
