import * as $protobuf from "protobufjs";
import Long = require("long");
/** Namespace archiveteam_tracker. */
export namespace archiveteam_tracker {

    /** Properties of a WarriorMeta. */
    interface IWarriorMeta {

        /** WarriorMeta userAgent */
        userAgent?: (string|null);

        /** WarriorMeta version */
        version?: (string|null);
    }

    /** Represents a WarriorMeta. */
    class WarriorMeta implements IWarriorMeta {

        /**
         * Constructs a new WarriorMeta.
         * @param [properties] Properties to set
         */
        constructor(properties?: archiveteam_tracker.IWarriorMeta);

        /** WarriorMeta userAgent. */
        public userAgent: string;

        /** WarriorMeta version. */
        public version: string;

        /**
         * Creates a new WarriorMeta instance using the specified properties.
         * @param [properties] Properties to set
         * @returns WarriorMeta instance
         */
        public static create(properties?: archiveteam_tracker.IWarriorMeta): archiveteam_tracker.WarriorMeta;

        /**
         * Encodes the specified WarriorMeta message. Does not implicitly {@link archiveteam_tracker.WarriorMeta.verify|verify} messages.
         * @param message WarriorMeta message or plain object to encode
         * @param [writer] Writer to encode to
         * @returns Writer
         */
        public static encode(message: archiveteam_tracker.IWarriorMeta, writer?: $protobuf.Writer): $protobuf.Writer;

        /**
         * Encodes the specified WarriorMeta message, length delimited. Does not implicitly {@link archiveteam_tracker.WarriorMeta.verify|verify} messages.
         * @param message WarriorMeta message or plain object to encode
         * @param [writer] Writer to encode to
         * @returns Writer
         */
        public static encodeDelimited(message: archiveteam_tracker.IWarriorMeta, writer?: $protobuf.Writer): $protobuf.Writer;

        /**
         * Decodes a WarriorMeta message from the specified reader or buffer.
         * @param reader Reader or buffer to decode from
         * @param [length] Message length if known beforehand
         * @returns WarriorMeta
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        public static decode(reader: ($protobuf.Reader|Uint8Array), length?: number): archiveteam_tracker.WarriorMeta;

        /**
         * Decodes a WarriorMeta message from the specified reader or buffer, length delimited.
         * @param reader Reader or buffer to decode from
         * @returns WarriorMeta
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        public static decodeDelimited(reader: ($protobuf.Reader|Uint8Array)): archiveteam_tracker.WarriorMeta;

        /**
         * Verifies a WarriorMeta message.
         * @param message Plain object to verify
         * @returns `null` if valid, otherwise the reason why it is not
         */
        public static verify(message: { [k: string]: any }): (string|null);

        /**
         * Creates a WarriorMeta message from a plain object. Also converts values to their respective internal types.
         * @param object Plain object
         * @returns WarriorMeta
         */
        public static fromObject(object: { [k: string]: any }): archiveteam_tracker.WarriorMeta;

        /**
         * Creates a plain object from a WarriorMeta message. Also converts values to other types if specified.
         * @param message WarriorMeta
         * @param [options] Conversion options
         * @returns Plain object
         */
        public static toObject(message: archiveteam_tracker.WarriorMeta, options?: $protobuf.IConversionOptions): { [k: string]: any };

        /**
         * Converts this WarriorMeta to JSON.
         * @returns JSON object
         */
        public toJSON(): { [k: string]: any };

        /**
         * Gets the default type url for WarriorMeta
         * @param [typeUrlPrefix] your custom typeUrlPrefix(default "type.googleapis.com")
         * @returns The default type url
         */
        public static getTypeUrl(typeUrlPrefix?: string): string;
    }

    /** Properties of a TrackerStats. */
    interface ITrackerStats {

        /** TrackerStats values */
        values?: ({ [k: string]: number }|null);

        /** TrackerStats queues */
        queues?: ({ [k: string]: number }|null);
    }

    /** Represents a TrackerStats. */
    class TrackerStats implements ITrackerStats {

        /**
         * Constructs a new TrackerStats.
         * @param [properties] Properties to set
         */
        constructor(properties?: archiveteam_tracker.ITrackerStats);

        /** TrackerStats values. */
        public values: { [k: string]: number };

        /** TrackerStats queues. */
        public queues: { [k: string]: number };

        /**
         * Creates a new TrackerStats instance using the specified properties.
         * @param [properties] Properties to set
         * @returns TrackerStats instance
         */
        public static create(properties?: archiveteam_tracker.ITrackerStats): archiveteam_tracker.TrackerStats;

        /**
         * Encodes the specified TrackerStats message. Does not implicitly {@link archiveteam_tracker.TrackerStats.verify|verify} messages.
         * @param message TrackerStats message or plain object to encode
         * @param [writer] Writer to encode to
         * @returns Writer
         */
        public static encode(message: archiveteam_tracker.ITrackerStats, writer?: $protobuf.Writer): $protobuf.Writer;

        /**
         * Encodes the specified TrackerStats message, length delimited. Does not implicitly {@link archiveteam_tracker.TrackerStats.verify|verify} messages.
         * @param message TrackerStats message or plain object to encode
         * @param [writer] Writer to encode to
         * @returns Writer
         */
        public static encodeDelimited(message: archiveteam_tracker.ITrackerStats, writer?: $protobuf.Writer): $protobuf.Writer;

        /**
         * Decodes a TrackerStats message from the specified reader or buffer.
         * @param reader Reader or buffer to decode from
         * @param [length] Message length if known beforehand
         * @returns TrackerStats
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        public static decode(reader: ($protobuf.Reader|Uint8Array), length?: number): archiveteam_tracker.TrackerStats;

        /**
         * Decodes a TrackerStats message from the specified reader or buffer, length delimited.
         * @param reader Reader or buffer to decode from
         * @returns TrackerStats
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        public static decodeDelimited(reader: ($protobuf.Reader|Uint8Array)): archiveteam_tracker.TrackerStats;

        /**
         * Verifies a TrackerStats message.
         * @param message Plain object to verify
         * @returns `null` if valid, otherwise the reason why it is not
         */
        public static verify(message: { [k: string]: any }): (string|null);

        /**
         * Creates a TrackerStats message from a plain object. Also converts values to their respective internal types.
         * @param object Plain object
         * @returns TrackerStats
         */
        public static fromObject(object: { [k: string]: any }): archiveteam_tracker.TrackerStats;

        /**
         * Creates a plain object from a TrackerStats message. Also converts values to other types if specified.
         * @param message TrackerStats
         * @param [options] Conversion options
         * @returns Plain object
         */
        public static toObject(message: archiveteam_tracker.TrackerStats, options?: $protobuf.IConversionOptions): { [k: string]: any };

        /**
         * Converts this TrackerStats to JSON.
         * @returns JSON object
         */
        public toJSON(): { [k: string]: any };

        /**
         * Gets the default type url for TrackerStats
         * @param [typeUrlPrefix] your custom typeUrlPrefix(default "type.googleapis.com")
         * @returns The default type url
         */
        public static getTypeUrl(typeUrlPrefix?: string): string;
    }

    /** Properties of a TrackerEvent. */
    interface ITrackerEvent {

        /** TrackerEvent project */
        project?: (string|null);

        /** TrackerEvent downloader */
        downloader?: (string|null);

        /** TrackerEvent logChannel */
        logChannel?: (string|null);

        /** TrackerEvent timestamp */
        timestamp?: (number|null);

        /** TrackerEvent bytes */
        bytes?: (number|Long|null);

        /** TrackerEvent sizeMb */
        sizeMb?: (number|null);

        /** TrackerEvent valid */
        valid?: (boolean|null);

        /** TrackerEvent isDuplicate */
        isDuplicate?: (boolean|null);

        /** TrackerEvent item */
        item?: (string|null);

        /** TrackerEvent items */
        items?: (string[]|null);

        /** TrackerEvent moveItems */
        moveItems?: (string[]|null);

        /** TrackerEvent itemRtts */
        itemRtts?: (number[]|null);

        /** TrackerEvent WarriorUserAgent */
        WarriorUserAgent?: (string|null);

        /** TrackerEvent WarriorVersion */
        WarriorVersion?: (string|null);

        /** TrackerEvent queueStats */
        queueStats?: ({ [k: string]: number }|null);

        /** TrackerEvent counts */
        counts?: ({ [k: string]: number }|null);

        /** TrackerEvent trackerStats */
        trackerStats?: (archiveteam_tracker.ITrackerStats|null);

        /** TrackerEvent domainBytes */
        domainBytes?: ({ [k: string]: (number|Long) }|null);
    }

    /** Represents a TrackerEvent. */
    class TrackerEvent implements ITrackerEvent {

        /**
         * Constructs a new TrackerEvent.
         * @param [properties] Properties to set
         */
        constructor(properties?: archiveteam_tracker.ITrackerEvent);

        /** TrackerEvent project. */
        public project: string;

        /** TrackerEvent downloader. */
        public downloader: string;

        /** TrackerEvent logChannel. */
        public logChannel: string;

        /** TrackerEvent timestamp. */
        public timestamp: number;

        /** TrackerEvent bytes. */
        public bytes: (number|Long);

        /** TrackerEvent sizeMb. */
        public sizeMb: number;

        /** TrackerEvent valid. */
        public valid: boolean;

        /** TrackerEvent isDuplicate. */
        public isDuplicate: boolean;

        /** TrackerEvent item. */
        public item: string;

        /** TrackerEvent items. */
        public items: string[];

        /** TrackerEvent moveItems. */
        public moveItems: string[];

        /** TrackerEvent itemRtts. */
        public itemRtts: number[];

        /** TrackerEvent WarriorUserAgent. */
        public WarriorUserAgent: string;

        /** TrackerEvent WarriorVersion. */
        public WarriorVersion: string;

        /** TrackerEvent queueStats. */
        public queueStats: { [k: string]: number };

        /** TrackerEvent counts. */
        public counts: { [k: string]: number };

        /** TrackerEvent trackerStats. */
        public trackerStats?: (archiveteam_tracker.ITrackerStats|null);

        /** TrackerEvent domainBytes. */
        public domainBytes: { [k: string]: (number|Long) };

        /**
         * Creates a new TrackerEvent instance using the specified properties.
         * @param [properties] Properties to set
         * @returns TrackerEvent instance
         */
        public static create(properties?: archiveteam_tracker.ITrackerEvent): archiveteam_tracker.TrackerEvent;

        /**
         * Encodes the specified TrackerEvent message. Does not implicitly {@link archiveteam_tracker.TrackerEvent.verify|verify} messages.
         * @param message TrackerEvent message or plain object to encode
         * @param [writer] Writer to encode to
         * @returns Writer
         */
        public static encode(message: archiveteam_tracker.ITrackerEvent, writer?: $protobuf.Writer): $protobuf.Writer;

        /**
         * Encodes the specified TrackerEvent message, length delimited. Does not implicitly {@link archiveteam_tracker.TrackerEvent.verify|verify} messages.
         * @param message TrackerEvent message or plain object to encode
         * @param [writer] Writer to encode to
         * @returns Writer
         */
        public static encodeDelimited(message: archiveteam_tracker.ITrackerEvent, writer?: $protobuf.Writer): $protobuf.Writer;

        /**
         * Decodes a TrackerEvent message from the specified reader or buffer.
         * @param reader Reader or buffer to decode from
         * @param [length] Message length if known beforehand
         * @returns TrackerEvent
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        public static decode(reader: ($protobuf.Reader|Uint8Array), length?: number): archiveteam_tracker.TrackerEvent;

        /**
         * Decodes a TrackerEvent message from the specified reader or buffer, length delimited.
         * @param reader Reader or buffer to decode from
         * @returns TrackerEvent
         * @throws {Error} If the payload is not a reader or valid buffer
         * @throws {$protobuf.util.ProtocolError} If required fields are missing
         */
        public static decodeDelimited(reader: ($protobuf.Reader|Uint8Array)): archiveteam_tracker.TrackerEvent;

        /**
         * Verifies a TrackerEvent message.
         * @param message Plain object to verify
         * @returns `null` if valid, otherwise the reason why it is not
         */
        public static verify(message: { [k: string]: any }): (string|null);

        /**
         * Creates a TrackerEvent message from a plain object. Also converts values to their respective internal types.
         * @param object Plain object
         * @returns TrackerEvent
         */
        public static fromObject(object: { [k: string]: any }): archiveteam_tracker.TrackerEvent;

        /**
         * Creates a plain object from a TrackerEvent message. Also converts values to other types if specified.
         * @param message TrackerEvent
         * @param [options] Conversion options
         * @returns Plain object
         */
        public static toObject(message: archiveteam_tracker.TrackerEvent, options?: $protobuf.IConversionOptions): { [k: string]: any };

        /**
         * Converts this TrackerEvent to JSON.
         * @returns JSON object
         */
        public toJSON(): { [k: string]: any };

        /**
         * Gets the default type url for TrackerEvent
         * @param [typeUrlPrefix] your custom typeUrlPrefix(default "type.googleapis.com")
         * @returns The default type url
         */
        public static getTypeUrl(typeUrlPrefix?: string): string;
    }
}
